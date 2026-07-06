package healthcheck

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/dexnotify"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/openclaw"
)

// defaultRecoveryGracePeriod is how long a row must sit in an intermediate tradeflow
// state before the recovery worker acts on it — long enough that a still-running
// synchronous TradeFlow.Run cannot be mistaken for an abandoned row.
const defaultRecoveryGracePeriod = 3 * time.Minute

// TradeflowRecoverer drives stuck/failed tradeflow rows forward. Implemented by
// *tradeflow.TradeFlow; declared here so healthcheck depends only on what it needs.
type TradeflowRecoverer interface {
	ResumeFromTradeflowStarted(ctx context.Context, tx *db.Transaction) error
	ResumeWithdrawal(ctx context.Context, tx *db.Transaction) error
	RecoverHedge(ctx context.Context, tx *db.Transaction) error
}

// HealthChecker queries the DB for incomplete transactions and verifies their
// actual state against external systems, advancing the status where appropriate.
type HealthChecker struct {
	repo                *db.TransactionRepository
	openclaw            *openclaw.Client
	btc                 *bitcoin.NetworkDataFetcher
	hodlhodl            *hodlhodl.Client
	bisq                *bisq.Client
	resolver            *dexaccount.PaymentResolver
	telegram            *dexnotify.Service
	anomaly             *dexnotify.AnomalyNotifier
	recoverer           TradeflowRecoverer
	recoveryGracePeriod time.Duration
}

// NewHealthChecker creates a health checker. recoverer drives recovery of stuck
// tradeflow rows; it may be nil to disable tradeflow recovery (those states then
// no-op rather than erroring).
func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client, btc *bitcoin.NetworkDataFetcher, hh *hodlhodl.Client, recoverer TradeflowRecoverer) *HealthChecker {
	return &HealthChecker{
		repo:                repo,
		openclaw:            oc,
		btc:                 btc,
		hodlhodl:            hh,
		recoverer:           recoverer,
		recoveryGracePeriod: defaultRecoveryGracePeriod,
	}
}

// WithRecoveryGracePeriod overrides the default grace period (e.g. from env).
func (h *HealthChecker) WithRecoveryGracePeriod(d time.Duration) *HealthChecker {
	if d > 0 {
		h.recoveryGracePeriod = d
	}
	return h
}

// WithPaymentResolver wires the resolver used to drive the Robosats trade-chat conversation
// (send the payment-details request, capture the counterparty's reply). Without it, chat-venue
// rows simply no-op in the post-take checks.
func (h *HealthChecker) WithPaymentResolver(r *dexaccount.PaymentResolver) *HealthChecker {
	h.resolver = r
	return h
}

// WithTelegramRelay wires the trade-chat relay and anomaly notifier.
func (h *HealthChecker) WithTelegramRelay(svc *dexnotify.Service, anomaly *dexnotify.AnomalyNotifier) *HealthChecker {
	h.telegram = svc
	h.anomaly = anomaly
	return h
}

// WithBisq wires the bisq1 client used for structured-venue anomaly checks.
func (h *HealthChecker) WithBisq(bq *bisq.Client) *HealthChecker {
	h.bisq = bq
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

// Run finds all incomplete transactions and runs a status-specific verification
// for each one.
func (h *HealthChecker) Run(ctx context.Context) error {
	transactions, err := h.repo.GetIncompleteTransactions()
	if err != nil {
		return fmt.Errorf("healthcheck: failed to fetch incomplete transactions: %w", err)
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
	switch tx.Status {
	case db.StatusFiatToCexInitiated:
		return h.verifyFiatToCexInitiated(ctx, tx)
	case db.StatusFiatToCexArrived:
		return h.verifyFiatToCexArrived(ctx, tx)
	case db.StatusCexTradeflowStarted:
		return h.verifyCexTradeflowStarted(ctx, tx)
	case db.StatusHedgeFailedNeedsReview:
		return h.verifyHedgeFailedNeedsReview(ctx, tx)
	case db.StatusCexTradeflowNeedsReview:
		return nil // alert already fired when the row entered this state; park it.
	case db.StatusCexTradeSuccessful:
		return h.verifyCexTradeSuccessful(ctx, tx)
	case db.StatusCexCryptoWithdrawalInitiated:
		return h.verifyCexCryptoWithdrawalInitiated(ctx, tx)
	case db.StatusCexCryptoWithdrawalArrived:
		return h.verifyCexCryptoWithdrawalArrived(ctx, tx)
	case db.StatusDexPositionOpen:
		return h.verifyDexPositionOpen(ctx, tx)
	case db.StatusDexPositionTaken:
		return h.verifyDexPositionTaken(ctx, tx)
	case db.StatusDexFiatHasArrived:
		return h.verifyDexFiatHasArrived(ctx, tx)
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

// verifyDexPositionOpen confirms the DEX contract is active after LND→DEX relay.
func (h *HealthChecker) verifyDexPositionOpen(ctx context.Context, tx *db.Transaction) error {
	switch tx.Dex {
	case db.DexHodlHodl:
		if contractID, err := dexContractID(tx); err == nil && contractID != "" {
			active, err := h.hodlhodl.ContractHasCounterparty(ctx, contractID)
			if err != nil {
				return fmt.Errorf("verifyDexPositionOpen: %w", err)
			}
			if active {
				tx.Status = db.StatusDexPositionTaken
				return h.repo.UpdateTransaction(tx)
			}
			return nil
		}
		offerID, err := dexOfferID(tx)
		if err != nil {
			return fmt.Errorf("verifyDexPositionOpen: %w", err)
		}
		live, err := h.hodlhodl.IsOfferLive(ctx, offerID)
		if err != nil {
			return fmt.Errorf("verifyDexPositionOpen: %w", err)
		}
		if !live {
			tx.Status = db.StatusDexPositionTaken
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexRoboSats, db.DexBisq2:
		// Chat venues: send the payment-details request once and capture the counterparty's reply.
		return h.captureChatPayment(ctx, tx)
	case db.DexBisq:
		return h.checkBisqAnomaly(ctx, tx)
	default:
		return fmt.Errorf("verifyDexPositionOpen: unknown DEX %q", tx.Dex)
	}
	return nil
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
	if trade.HasFailed || trade.ErrorMessage != "" {
		detail := trade.ErrorMessage
		if detail == "" {
			detail = "trade state=" + trade.State
		}
		return h.notifyAnomaly(ctx, string(tx.Dex), tradeID, "bisq trade failure", detail)
	}
	return nil
}

// verifyDexPositionTaken confirms the trade is in progress on the DEX.
// For HodlHodl, checks whether the buyer has marked the contract as paid (fiat sent).
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
		paid, err := h.hodlhodl.ContractIsPaid(ctx, contractID)
		if err != nil {
			return fmt.Errorf("verifyDexPositionTaken: %w", err)
		}
		if paid {
			tx.Status = db.StatusDexFiatHasArrived
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexBisq:
		return h.checkBisqAnomaly(ctx, tx)
	case db.DexRoboSats:
		// TODO: implement RoboSats contract status check
	default:
		return fmt.Errorf("verifyDexPositionTaken: unknown DEX %q", tx.Dex)
	}
	return nil
}

// verifyDexFiatHasArrived asks the local openclaw instance whether the fiat
// payment has actually arrived in the bank account. Advances to
// DexPositionReleased if confirmed, so the BTC release can be triggered.
func (h *HealthChecker) verifyDexFiatHasArrived(ctx context.Context, tx *db.Transaction) error {
	arrived, err := h.openclaw.PollBankTxArrivedFromDex(ctx, fmt.Sprintf("tx-%d", tx.ID), 5*time.Second)
	if err != nil {
		return fmt.Errorf("verifyDexFiatHasArrived: openclaw poll failed: %w", err)
	}
	if !arrived {
		return fmt.Errorf("verifyDexFiatHasArrived: fiat not yet confirmed in bank account")
	}
	// Fiat confirmed — caller should now trigger BTC release on the DEX.
	return nil
}

// verifyDexPositionReleased connects to the DEX specified in tx.Dex and checks
// whether the BTC has actually been released to the buyer.
func (h *HealthChecker) verifyDexPositionReleased(ctx context.Context, tx *db.Transaction) error {
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
			tx.Status = db.StatusDexPositionReleased
			tx.TransactionComplete = true
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexBisq, db.DexRoboSats:
		// TODO: implement Bisq / RoboSats release confirmation
	default:
		return fmt.Errorf("verifyDexPositionReleased: unknown DEX %q", tx.Dex)
	}
	return nil
}

// verifyFiatToCexInitiated asks openclaw (the bank gateway) whether the fiat
// wire to the CEX has been sent. The transaction ID is used as the openclaw
// reference code, matching the pattern used for DEX fiat checks.
func (h *HealthChecker) verifyFiatToCexInitiated(ctx context.Context, tx *db.Transaction) error {
	arrived, err := h.openclaw.BankTxHasBeenSentToCex(ctx, fmt.Sprintf("tx-%d", tx.ID))
	if err != nil {
		return fmt.Errorf("verifyFiatToCexInitiated: openclaw check failed: %w", err)
	}
	if arrived {
		tx.Status = db.StatusFiatToCexArrived
		return h.repo.UpdateTransaction(tx)
	}
	return nil
}

// verifyFiatToCexArrived is handled by the tradeflow orchestrator for bid rows.
func (h *HealthChecker) verifyFiatToCexArrived(_ context.Context, _ *db.Transaction) error {
	return nil
}
