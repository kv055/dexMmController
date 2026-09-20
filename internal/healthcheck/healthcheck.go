package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/dexnotify"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
)

// defaultRecoveryGracePeriod is how long a row must sit in an intermediate tradeflow
// state before the recovery worker acts on it — long enough that a still-running
// synchronous TradeFlow.Run cannot be mistaken for an abandoned row.
const (
	defaultRecoveryGracePeriod     = 3 * time.Minute
	defaultBisqFiatEscalationDelay = 2 * time.Hour
)

// TradeflowRecoverer drives stuck/failed tradeflow rows forward. Implemented by
// *tradeflow.TradeFlow; declared here so healthcheck depends only on what it needs.
type TradeflowRecoverer interface {
	ResumeFromTradeflowStarted(ctx context.Context, tx *db.Transaction) error
	ResumeWithdrawal(ctx context.Context, tx *db.Transaction) error
	RecoverHedge(ctx context.Context, tx *db.Transaction) error
}

type transactionStore interface {
	GetTransactionsForHealthcheck() ([]db.Transaction, error)
	UpdateTransaction(tx *db.Transaction) error
}

type bitcoinTransactionSource interface {
	FetchTransaction(ctx context.Context, txid string) (*bitcoin.TxStatus, error)
}

type bisqPositionSource interface {
	GetPosition(ctx context.Context, tradeID string) (*bisq.Trade, error)
}

type hodlPositionSource interface {
	GetContract(ctx context.Context, contractID string) (*hodlhodl.Contract, error)
	IsContractCompleted(ctx context.Context, contractID string) (bool, error)
}

type anomalyNotifier interface {
	Enabled() bool
	NotifyAnomaly(ctx context.Context, exchange, tradeID, reason, detail string) error
}

type paymentResolver interface {
	IsStructuredVenue(exchange string) bool
	FetchCounterparty(ctx context.Context, exchange string, execution map[string]any) (json.RawMessage, error)
	CaptureChatPayment(ctx context.Context, req dexaccount.ChatRequest) (json.RawMessage, error)
}

type bisqFiatEscalationState struct {
	firstSeen time.Time
	notifying bool
	notified  bool
}

type anomalyDeliveryState struct {
	notifying bool
	notified  bool
}

// mostroReconciler has no command or payment methods: health checks only observe.
type mostroReconciler interface {
	Reconcile(context.Context, uint64, string) (bool, error)
}

type tradeChatRegistrar interface {
	RegisterTradeChat(context.Context, string, string) error
}

// HealthChecker queries incomplete transactions and verifies their actual state
// against external systems, advancing their status where appropriate.
type HealthChecker struct {
	mostro                  mostroReconciler
	repo                    transactionStore
	btc                     bitcoinTransactionSource
	hodlhodl                hodlPositionSource
	bisq                    bisqPositionSource
	resolver                paymentResolver
	telegram                tradeChatRegistrar
	anomaly                 anomalyNotifier
	recoverer               TradeflowRecoverer
	recoveryGracePeriod     time.Duration
	bisqFiatEscalationDelay time.Duration
	bisqFiatEscalationMu    sync.Mutex
	bisqFiatEscalations     map[string]bisqFiatEscalationState
	bisqReleaseAnomalyMu    sync.Mutex
	bisqReleaseAnomalies    map[string]anomalyDeliveryState
	now                     func() time.Time
}

// NewHealthChecker creates a health checker. recoverer drives recovery of stuck
// tradeflow rows; it may be nil to disable tradeflow recovery (those states then
// no-op rather than erroring).
func NewHealthChecker(repo *db.TransactionRepository, btc *bitcoin.NetworkDataFetcher, hh *hodlhodl.Client, recoverer TradeflowRecoverer) *HealthChecker {
	h := &HealthChecker{
		hodlhodl:                hh,
		recoverer:               recoverer,
		recoveryGracePeriod:     defaultRecoveryGracePeriod,
		bisqFiatEscalationDelay: defaultBisqFiatEscalationDelay,
		bisqFiatEscalations:     make(map[string]bisqFiatEscalationState),
		bisqReleaseAnomalies:    make(map[string]anomalyDeliveryState),
		now:                     time.Now,
	}
	if repo != nil {
		h.repo = repo
	}
	if btc != nil {
		h.btc = btc
	}
	return h
}

// WithRecoveryGracePeriod overrides the default grace period (e.g. from env).
func (h *HealthChecker) WithRecoveryGracePeriod(d time.Duration) *HealthChecker {
	if d > 0 {
		h.recoveryGracePeriod = d
	}
	return h
}

// WithBisqFiatEscalationDelay configures how long a Bisq fiat-sent signal must
// remain unmatched before alerting the operator. Zero or negative disables it.
func (h *HealthChecker) WithBisqFiatEscalationDelay(d time.Duration) *HealthChecker {
	h.bisqFiatEscalationDelay = d
	return h
}

// WithPaymentResolver wires the resolver used to drive the Robosats trade-chat conversation
// (send the payment-details request, capture the counterparty's reply). Without it, chat-venue
// rows simply no-op in the post-take checks.
func (h *HealthChecker) WithPaymentResolver(r *dexaccount.PaymentResolver) *HealthChecker {
	h.resolver = r
	return h
}

// WithMostro wires read-only daemon and wallet reconciliation.
func (h *HealthChecker) WithMostro(m mostroReconciler) *HealthChecker {
	h.mostro = m
	return h
}

// WithTelegramRelay wires the trade-chat relay and anomaly notifier.
func (h *HealthChecker) WithTelegramRelay(svc *dexnotify.Service, anomaly *dexnotify.AnomalyNotifier) *HealthChecker {
	if svc == nil {
		h.telegram = nil
	} else {
		h.telegram = svc
	}
	if anomaly == nil {
		h.anomaly = nil
	} else {
		h.anomaly = anomaly
	}
	return h
}

// WithBisq wires the bisq1 client used for structured-venue anomaly checks.
func (h *HealthChecker) WithBisq(bq *bisq.Client) *HealthChecker {
	if bq == nil {
		h.bisq = nil
	} else {
		h.bisq = bq
	}
	return h
}

// RunPeriodic executes healthcheck on interval until ctx is cancelled.
func (h *HealthChecker) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := h.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("healthcheck: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Run finds all active and released-but-unverified transactions and runs a
// status-specific verification for each one.
func (h *HealthChecker) Run(ctx context.Context) error {
	transactions, err := h.repo.GetTransactionsForHealthcheck()
	if err != nil {
		return fmt.Errorf("healthcheck: failed to fetch transactions for verification: %w", err)
	}

	for i := range transactions {
		tx := &transactions[i]
		if err := h.verify(ctx, tx); err != nil {
			// Log and continue — one failing transaction should not block the others.
			fmt.Printf("healthcheck: tx %d (%s): %v\n", tx.ID, tx.Status, err)
		}
	}
	return nil
}

// verify dispatches to the appropriate scenario based on the current status.
func (h *HealthChecker) verify(ctx context.Context, tx *db.Transaction) error {
	if db.IsDurableRoboSats(tx) && tx.Status != db.StatusDexFiatHasArrived && tx.Status != db.StatusDexPositionReleased {
		return nil // The durable RoboSats runner owns funding, recovery and chat.
	}
	if db.BisqManaged(tx) && tx.Status != db.StatusDexPositionTaken && tx.Status != db.StatusDexFiatHasArrived && tx.Status != db.StatusDexPositionReleased {
		return nil // The versioned executor owns all pre-take financial actions.
	}
	switch tx.Status {
	case db.StatusTransactionNeedsReview:
		return nil // Unclassifiable legacy rows require explicit operator reconciliation.
	case db.StatusCexTradeflowStarted:
		return h.verifyCexTradeflowStarted(ctx, tx)
	case db.StatusHedgeFailedNeedsReview:
		return h.verifyHedgeFailedNeedsReview(ctx, tx)
	case db.StatusCexTradeflowNeedsReview:
		return nil // alert already fired when the row entered this state; park it.
	case db.StatusHedgelessTradeStarted, db.StatusHedgelessFundingInitiated:
		return nil // owned by tradeflow.RunRefillWorker; avoid competing money movement.
	case db.StatusHedgelessPreparationFailed:
		return nil // Terminal audit row; normally excluded by transaction_complete.
	case db.StatusLiquidityPreparationNeedsReview, db.StatusHedgelessFundingNeedsReview, db.StatusDexTakenFundingNeedsReview:
		return nil // Parked until durable external state is reconciled.
	case db.StatusCexTradeSuccessful:
		return h.verifyCexTradeSuccessful(ctx, tx)
	case db.StatusCexCryptoWithdrawalInitiated:
		return h.verifyCexCryptoWithdrawalInitiated(ctx, tx)
	case db.StatusCexCryptoWithdrawalArrived:
		return h.verifyCexCryptoWithdrawalArrived(ctx, tx)
	case db.StatusDexPositionTaken:
		return h.verifyDexPositionTaken(ctx, tx)
	case db.StatusDexFiatHasArrived:
		return nil // BTC release is handled by dexrelease after external confirmation.
	case db.StatusDexPositionReleased:
		return h.verifyDexPositionReleased(ctx, tx)
	default:
		return fmt.Errorf("unknown status %q", tx.Status)
	}
}

// withinGrace reports whether a row is too fresh to recover — it may be a still-in-
// flight synchronous TradeFlow.Run rather than an abandoned cycle.
func (h *HealthChecker) withinGrace(tx *db.Transaction) bool {
	return time.Since(tx.UpdatedAt) < h.recoveryGracePeriod
}

// verifyCexTradeflowStarted recovers a row whose tradeflow died before the hedge:
// resume from the hedge step if the spot fill is recorded, else park for review.
func (h *HealthChecker) verifyCexTradeflowStarted(ctx context.Context, tx *db.Transaction) error {
	if h.recoverer == nil || h.withinGrace(tx) {
		return nil
	}
	return h.recoverer.ResumeFromTradeflowStarted(ctx, tx)
}

// verifyCexTradeSuccessful recovers a row whose spot+hedge completed but whose
// withdrawal was never sent (or failed): retry the withdrawal idempotently.
func (h *HealthChecker) verifyCexTradeSuccessful(ctx context.Context, tx *db.Transaction) error {
	if h.recoverer == nil || h.withinGrace(tx) {
		return nil
	}
	return h.recoverer.ResumeWithdrawal(ctx, tx)
}

// verifyHedgeFailedNeedsReview re-checks the venue: if the hedge is now open the row
// is advanced to CexTradeSuccessful, otherwise it stays for manual reconciliation.
func (h *HealthChecker) verifyHedgeFailedNeedsReview(ctx context.Context, tx *db.Transaction) error {
	if h.recoverer == nil {
		return nil
	}
	return h.recoverer.RecoverHedge(ctx, tx)
}

// verifyCexCryptoWithdrawalInitiated is handled by dexrelay for bid tradeflow rows.
func (h *HealthChecker) verifyCexCryptoWithdrawalInitiated(_ context.Context, _ *db.Transaction) error {
	return nil
}

// verifyCexCryptoWithdrawalArrived is handled by dexrelay (confirmation + relay).
func (h *HealthChecker) verifyCexCryptoWithdrawalArrived(_ context.Context, _ *db.Transaction) error {
	return nil
}

// dexOfferID extracts the offer_id from DexOfferCreation (execution or selected_offer).
func dexOfferID(tx *db.Transaction) (string, error) {
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return "", fmt.Errorf("failed to parse DexOfferCreation: %w", err)
	}
	id := meta.OfferID()
	if id == "" {
		return "", fmt.Errorf("DexOfferCreation has no offer_id for tx %d", tx.ID)
	}
	return id, nil
}

// dexContractID extracts contract/trade/position id from DexOfferCreation.execution.
func dexContractID(tx *db.Transaction) (string, error) {
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return "", fmt.Errorf("failed to parse DexOfferCreation: %w", err)
	}
	id := meta.ContractID()
	if id == "" {
		return "", fmt.Errorf("DexOfferCreation has no contract_id for tx %d", tx.ID)
	}
	return id, nil
}

// captureChatPayment drives the chat-venue conversation for a row: send the templated
// payment-details request once and store the counterparty's inbound reply on the row. The fiat
// controller reads counterparty_payment_info to match and decide release vs dispute.
func (h *HealthChecker) captureChatPayment(ctx context.Context, tx *db.Transaction) error {
	if h.resolver == nil {
		return nil
	}
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return fmt.Errorf("captureChatPayment: %w", err)
	}
	orderID := meta.ContractID()
	if orderID == "" {
		return nil // offer not taken / no order id yet — nothing to capture
	}
	exchange := meta.Exchange()
	if exchange == "" {
		exchange = string(tx.Dex)
	}
	info, err := h.resolver.CaptureChatPayment(ctx, dexaccount.ChatRequest{
		Exchange:         exchange,
		OrderID:          orderID,
		ExpectedAmount:   strconv.FormatFloat(tx.FiatAmount, 'f', 2, 64),
		ExpectedCurrency: tx.FiatCurrency,
		PaymentMethod:    tx.PaymentMethod,
		TradeDirection:   tx.ChatTradeDirection(),
	})
	if err != nil {
		return fmt.Errorf("captureChatPayment: %w", err)
	}
	tx.CounterpartyPaymentInfo = info
	if err := h.repo.UpdateTransaction(tx); err != nil {
		return err
	}
	if h.telegram != nil {
		if err := h.telegram.RegisterTradeChat(ctx, exchange, orderID); err != nil {
			return fmt.Errorf("captureChatPayment: telegram relay: %w", err)
		}
	}
	return nil
}

func (h *HealthChecker) notifyAnomaly(ctx context.Context, exchange, tradeID, reason, detail string) error {
	if h.anomaly == nil {
		return nil
	}
	return h.anomaly.NotifyAnomaly(ctx, exchange, tradeID, reason, detail)
}

func (h *HealthChecker) checkBisqAnomaly(ctx context.Context, tx *db.Transaction) error {
	if h.bisq == nil || h.anomaly == nil {
		return nil
	}
	tradeID, err := dexContractID(tx)
	if err != nil || tradeID == "" {
		return nil
	}
	trade, err := h.bisq.GetPosition(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("checkBisqAnomaly: %w", err)
	}
	if trade == nil {
		return nil
	}
	_, err = h.checkBisqTradeAnomaly(ctx, tx, tradeID, trade)
	return err
}

func (h *HealthChecker) checkBisqTradeAnomaly(ctx context.Context, tx *db.Transaction, tradeID string, trade *bisq.Trade) (bool, error) {
	if trade.HasFailed || trade.ErrorMessage != "" {
		detail := trade.ErrorMessage
		if detail == "" {
			detail = "trade state=" + trade.State
		}
		return true, h.notifyAnomaly(ctx, string(tx.Dex), tradeID, "bisq trade failure", detail)
	}
	return false, nil
}

func (h *HealthChecker) verifyBisqPositionTaken(ctx context.Context, tx *db.Transaction) error {
	if err := h.captureStructuredPayment(ctx, tx); err != nil {
		return err
	}
	if h.bisq == nil || h.anomaly == nil {
		return nil
	}
	tradeID, err := dexContractID(tx)
	if err != nil || tradeID == "" {
		return nil
	}
	trade, err := h.bisq.GetPosition(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionTaken: %w", err)
	}
	if trade == nil {
		return nil
	}
	failed, err := h.checkBisqTradeAnomaly(ctx, tx, tradeID, trade)
	if err != nil || failed {
		return err
	}
	return h.maybeEscalateBisqFiatSent(ctx, tx, tradeID, trade)
}

func (h *HealthChecker) captureStructuredPayment(ctx context.Context, tx *db.Transaction) error {
	if h.resolver == nil || len(tx.CounterpartyPaymentInfo) > 0 {
		return nil
	}
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return nil
	}
	exchange := meta.Exchange()
	if exchange == "" {
		exchange = string(tx.Dex)
	}
	if !h.resolver.IsStructuredVenue(exchange) {
		return nil
	}
	info, err := h.resolver.FetchCounterparty(ctx, exchange, meta.Execution)
	if err != nil {
		return nil // pending contracts retry next tick
	}
	if len(info) == 0 {
		return nil
	}
	tx.CounterpartyPaymentInfo = info
	return h.repo.UpdateTransaction(tx)
}

func (h *HealthChecker) maybeEscalateBisqFiatSent(ctx context.Context, tx *db.Transaction, tradeID string, trade *bisq.Trade) error {
	if h.bisqFiatEscalationDelay <= 0 || !trade.FiatSent() {
		return nil
	}
	now := time.Now()
	if h.now != nil {
		now = h.now()
	}

	h.bisqFiatEscalationMu.Lock()
	if h.bisqFiatEscalations == nil {
		h.bisqFiatEscalations = make(map[string]bisqFiatEscalationState)
	}
	state, observed := h.bisqFiatEscalations[tradeID]
	if !observed {
		h.bisqFiatEscalations[tradeID] = bisqFiatEscalationState{firstSeen: now}
		h.bisqFiatEscalationMu.Unlock()
		return nil
	}
	if state.notified || state.notifying || now.Sub(state.firstSeen) < h.bisqFiatEscalationDelay || !h.anomaly.Enabled() {
		h.bisqFiatEscalationMu.Unlock()
		return nil
	}
	state.notifying = true
	h.bisqFiatEscalations[tradeID] = state
	h.bisqFiatEscalationMu.Unlock()

	waiting := now.Sub(state.firstSeen).Round(time.Minute)
	detail := fmt.Sprintf("phase=%s state=%s fiat=%.2f %s fiat_sent_observed_for=%s",
		trade.Phase, trade.State, tx.FiatAmount, tx.FiatCurrency, waiting)
	err := h.notifyAnomaly(ctx, string(tx.Dex), tradeID,
		"buyer marked fiat sent but no bank notification matched; verify receipt manually before release", detail)

	h.bisqFiatEscalationMu.Lock()
	state = h.bisqFiatEscalations[tradeID]
	state.notifying = false
	if err == nil {
		state.notified = true
	}
	h.bisqFiatEscalations[tradeID] = state
	h.bisqFiatEscalationMu.Unlock()
	return err
}

func (h *HealthChecker) notifyBisqReleaseAnomalyOnce(ctx context.Context, tx *db.Transaction, tradeID, kind, reason, detail string) error {
	if h.anomaly == nil {
		return nil
	}
	key := tradeID + "\x00" + kind

	h.bisqReleaseAnomalyMu.Lock()
	if h.bisqReleaseAnomalies == nil {
		h.bisqReleaseAnomalies = make(map[string]anomalyDeliveryState)
	}
	state := h.bisqReleaseAnomalies[key]
	if state.notified || state.notifying || !h.anomaly.Enabled() {
		h.bisqReleaseAnomalyMu.Unlock()
		return nil
	}
	state.notifying = true
	h.bisqReleaseAnomalies[key] = state
	h.bisqReleaseAnomalyMu.Unlock()

	err := h.notifyAnomaly(ctx, string(tx.Dex), tradeID, reason, detail)

	h.bisqReleaseAnomalyMu.Lock()
	state = h.bisqReleaseAnomalies[key]
	state.notifying = false
	if err == nil {
		state.notified = true
	}
	h.bisqReleaseAnomalies[key] = state
	h.bisqReleaseAnomalyMu.Unlock()
	return err
}

// verifyDexPositionTaken monitors an already-taken, durably funded DEX trade.
// Venue paid/sent flags are informational only: only bank-side evidence may
// advance the row to DexFiatHasArrived and authorize BTC release.
func (h *HealthChecker) verifyDexPositionTaken(ctx context.Context, tx *db.Transaction) error {
	switch tx.Dex {
	case db.DexHodlHodl:
		contractID, err := dexContractID(tx)
		if err != nil {
			return fmt.Errorf("verifyDexPositionTaken: %w", err)
		}
		contract, err := h.hodlhodl.GetContract(ctx, contractID)
		if err != nil {
			return fmt.Errorf("verifyDexPositionTaken: %w", err)
		}
		if contract.Status == hodlhodl.ContractDisputed || contract.Status == hodlhodl.ContractCanceled {
			_ = h.notifyAnomaly(ctx, string(tx.Dex), contractID, "contract status "+string(contract.Status), "")
		}
	case db.DexBisq:
		return h.verifyBisqPositionTaken(ctx, tx)
	case db.DexMostro:
		complete, err := h.reconcileMostro(ctx, tx)
		if err != nil {
			return err
		}
		if complete {
			return fmt.Errorf("mostro: unexpected settlement before recorded fiat receipt")
		}
		if h.resolver == nil {
			return fmt.Errorf("mostro: payment chat resolver is not configured")
		}
		return h.captureChatPayment(ctx, tx)
	case db.DexRoboSats, db.DexBisq2:
		return h.captureChatPayment(ctx, tx)
	default:
		return fmt.Errorf("verifyDexPositionTaken: unknown DEX %q", tx.Dex)
	}
	return nil
}

// verifyDexPositionReleased connects to the DEX specified in tx.Dex and checks
// whether the BTC has actually been released to the buyer.
func (h *HealthChecker) verifyDexPositionReleased(ctx context.Context, tx *db.Transaction) error {
	// DEX release and hedgeless pool restoration are independent lifecycles.
	// Do not mark the transaction complete while its security deposit is still
	// returning (or needs operator reconciliation).
	if tx.TradeMode == "hedgeless_double_collateral" && tx.BTCPoolRefillStatus != "restored" {
		return nil
	}
	switch tx.Dex {
	case db.DexHodlHodl:
		contractID, err := dexContractID(tx)
		if err != nil {
			return fmt.Errorf("verifyDexPositionReleased: %w", err)
		}
		completed, err := h.hodlhodl.IsContractCompleted(ctx, contractID)
		if err != nil {
			return fmt.Errorf("verifyDexPositionReleased: %w", err)
		}
		if completed {
			tx.SetStatus(db.StatusDexPositionReleased, "")
			tx.TransactionComplete = true
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexBisq:
		return h.verifyBisqPositionReleased(ctx, tx)
	case db.DexMostro:
		complete, err := h.reconcileMostro(ctx, tx)
		if err != nil {
			return err
		}
		if complete {
			tx.TransactionComplete = true
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexRoboSats:
		// TODO: implement RoboSats release confirmation
	default:
		return fmt.Errorf("verifyDexPositionReleased: unknown DEX %q", tx.Dex)
	}
	return nil
}

func (h *HealthChecker) reconcileMostro(ctx context.Context, tx *db.Transaction) (bool, error) {
	if h.mostro == nil {
		return false, fmt.Errorf("mostro: reconciliation is not configured")
	}
	ref, err := dexContractID(tx)
	if err != nil {
		return false, err
	}
	complete, err := h.mostro.Reconcile(ctx, uint64(tx.ID), ref)
	if err != nil {
		_ = h.notifyAnomaly(ctx, string(tx.Dex), ref, "mostro reconciliation failed", err.Error())
		return false, err
	}
	return complete, nil
}

func (h *HealthChecker) verifyBisqPositionReleased(ctx context.Context, tx *db.Transaction) error {
	if tx.TransactionComplete {
		return nil
	}
	if h.bisq == nil {
		return fmt.Errorf("verifyBisqPositionReleased: bisq client is not configured")
	}
	tradeID, err := dexContractID(tx)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: %w", err)
	}
	trade, err := h.bisq.GetPosition(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: get trade: %w", err)
	}
	if trade == nil {
		return fmt.Errorf("verifyBisqPositionReleased: trade %s not found", tradeID)
	}
	if trade.HasFailed || strings.TrimSpace(trade.ErrorMessage) != "" {
		detail := fmt.Sprintf("phase=%s state=%s error=%s", trade.Phase, trade.State, strings.TrimSpace(trade.ErrorMessage))
		return h.notifyBisqReleaseAnomalyOnce(ctx, tx, tradeID, "trade_failure", "bisq payout verification failed", detail)
	}
	if !trade.PayoutPublished() {
		return nil
	}
	payoutTxID := strings.TrimSpace(trade.PayoutTxID)
	if payoutTxID == "" {
		return nil
	}

	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: %w", err)
	}
	persistedTxID := dexaccount.StringField(meta.Execution, "release_tx_id")
	if persistedTxID != "" && !strings.EqualFold(persistedTxID, payoutTxID) {
		detail := fmt.Sprintf("persisted_release_tx_id=%s daemon_payout_tx_id=%s", persistedTxID, payoutTxID)
		return h.notifyBisqReleaseAnomalyOnce(ctx, tx, tradeID, "payout_txid_mismatch", "bisq payout txid mismatch", detail)
	}
	if h.btc == nil {
		return fmt.Errorf("verifyBisqPositionReleased: bitcoin transaction source is not configured")
	}
	chainTx, err := h.btc.FetchTransaction(ctx, payoutTxID)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: fetch payout transaction: %w", err)
	}
	if chainTx == nil {
		return nil
	}
	if returnedTxID := strings.TrimSpace(chainTx.TxID); returnedTxID != "" && !strings.EqualFold(returnedTxID, payoutTxID) {
		detail := fmt.Sprintf("daemon_payout_tx_id=%s chain_result_tx_id=%s", payoutTxID, returnedTxID)
		return h.notifyBisqReleaseAnomalyOnce(ctx, tx, tradeID, "chain_txid_mismatch", "bitcoin lookup returned a different payout txid", detail)
	}
	chainStatus := strings.ToLower(strings.TrimSpace(chainTx.StatusLabel))
	if chainTx.Confirmed {
		chainStatus = "confirmed"
	}
	if !chainTx.Confirmed && chainStatus != "mempool" && chainStatus != "confirmed" {
		return nil
	}

	if meta.Execution == nil {
		meta.Execution = make(map[string]any)
	}
	verifiedAt := time.Now().UTC()
	if h.now != nil {
		verifiedAt = h.now().UTC()
	}
	meta.Execution["release_tx_id"] = payoutTxID
	meta.Execution["release_verified_at"] = verifiedAt.Format(time.RFC3339)
	meta.Execution["release_chain_status"] = chainStatus
	meta.Execution["release_confirmations"] = chainTx.Confirmations
	raw, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: marshal execution metadata: %w", err)
	}
	tx.DexOfferCreation = raw
	tx.SetStatus(db.StatusDexPositionReleased, "")
	tx.TransactionComplete = true
	if err := h.repo.UpdateTransaction(tx); err != nil {
		return fmt.Errorf("verifyBisqPositionReleased: persist verification: %w", err)
	}
	return nil
}
