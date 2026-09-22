package tradeflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/pretrade"
	lnd "dexMmController/internal/wallethub"
)

const (
	// Pool refill has its own lifecycle because restoring collateral continues
	// after the DEX position is already open:
	//
	//   pending -> spot_buy_pending -> spot_bought
	//           -> withdrawal_initiated -> awaiting_security_deposit -> restored
	//
	// Any state can be parked in needs_review when an external side effect may
	// have happened but cannot be proven from persisted receipts. In particular,
	// spot_buy_pending is an intent marker written before the non-idempotent market
	// order; the worker must never convert an ambiguous response into a second buy.
	refillPending             = "pending"
	refillSpotBuyPending      = "spot_buy_pending"
	refillSpotBought          = "spot_bought"
	refillWithdrawalInitiated = "withdrawal_initiated"
	refillAwaitingDeposit     = "awaiting_security_deposit"
	refillRestored            = "restored"
	refillNeedsReview         = "needs_review"
	hedgelessSatPerVByte      = int64(2)
	bisqFundingPollInterval   = 3 * time.Second
	bisqFundingPollTimeout    = 10 * time.Minute
	refillRecoveryGrace       = 3 * time.Minute
	maxRefillAttempts         = 2
	defaultRoutingFeeLimitSat = int64(100)
)

type hedgelessCEX interface {
	// MarketBuyBTCForRefill skips the liquidity-pool pre-check. That check has
	// already passed before funding, and must fail after the pool is spent.
	MarketBuyBTCForRefill(ctx context.Context, btcAmount float64, p pretrade.Params) (*cexaccount.SpotOrderResult, error)
	// LNDPoolBalanceBTC returns the form of liquidity consumed by this DEX:
	// confirmed on-chain BTC for Bisq/HodlHodl, and on-chain plus local channel
	// liquidity for RoboSats.
	LNDPoolBalanceBTC(ctx context.Context, dex string) (float64, error)
}

type activeRefillStore interface {
	GetActiveHedgelessRefills() ([]db.Transaction, error)
}

type withdrawalStatusService interface {
	GetWithdrawalStatus(ctx context.Context, rawProvider, withdrawalID, clientRequestID string) (*cexaccount.WithdrawalStatus, error)
}

// refillAttempt is the durable audit trail for one replacement purchase and
// withdrawal. Receipts are added as each irreversible boundary completes.
type refillAttempt struct {
	Attempt      int                         `json:"attempt"`
	RequestedBTC float64                     `json:"requested_btc"`
	Spot         *cexaccount.SpotOrderResult `json:"spot,omitempty"`
	Withdrawal   *WithdrawalResult           `json:"withdrawal,omitempty"`
	CreatedAt    time.Time                   `json:"created_at"`
}

type refillDetails struct {
	// LNDAddress is copied into the refill record so recovery does not depend on
	// whichever address the CEX account happens to whitelist later.
	LNDAddress string          `json:"lnd_address"`
	Attempts   []refillAttempt `json:"attempts"`
	// SecurityDepositReturn is written by the fiat-received release worker and
	// consumed here when deciding whether the original pool target can be
	// restored without buying the DEX collateral a second time.
	SecurityDepositReturn *securityDepositReturnDetails `json:"security_deposit_return,omitempty"`
}

type securityDepositReturnDetails struct {
	Method      string    `json:"method,omitempty"`
	Status      string    `json:"status,omitempty"`
	Destination string    `json:"destination,omitempty"`
	AmountBTC   float64   `json:"amount_btc,omitempty"`
	TxID        string    `json:"txid,omitempty"`
	Memo        string    `json:"memo,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// runHedgelessBid executes the double-collateral order:
//
//  1. reserve the singleton BTC pool and snapshot its pre-spend balance;
//  2. take/fund the DEX offer from LND immediately;
//  3. buy the spent BTC on the spot market and withdraw it back to LND.
//
// Steps 2 and 3 are intentionally the reverse of the hedgefull CEX-first flow.
// The replacement withdrawal may remain in progress after this request returns;
// RunRefillWorker owns confirmation and, if necessary, one corrective refill.
func (tf *TradeFlow) runHedgelessBid(ctx context.Context, order Order) (*Result, error) {
	if tf.store == nil {
		return nil, fmt.Errorf("tradeflow: hedgeless execution requires transaction persistence")
	}
	if tf.hedgelessDEX == nil || tf.hedgelessWallet == nil {
		return nil, fmt.Errorf("tradeflow: hedgeless DEX/LND execution is not configured")
	}
	hc, ok := tf.svc.(hedgelessCEX)
	if !ok {
		return nil, fmt.Errorf("tradeflow: hedgeless CEX refill service is not configured")
	}
	if rows, ok := tf.store.(activeRefillStore); ok {
		// Only one trade may spend from the shared LND pool at a time. The
		// database partial unique index enforces the same rule across processes;
		// this read provides a useful error before attempting the insert.
		active, err := rows.GetActiveHedgelessRefills()
		if err != nil {
			return nil, err
		}
		for _, row := range active {
			if order.TransactionID == 0 || row.ID != order.TransactionID {
				return nil, fmt.Errorf("tradeflow: hedgeless liquidity pool is reserved by transaction %d (%s)", row.ID, row.BTCPoolRefillStatus)
			}
		}
	}

	preTrade := pretrade.Params{
		Provider: order.Provider, FiatCurrency: order.FiatCurrency,
		RequiredCapital:      order.RequiredCapital,
		RequiredBTCLiquidity: order.RequiredBTCLiquidity,
		PaymentMethod:        order.PaymentMethod, PaymentMethods: order.PaymentMethods,
		Dex: order.Dex, MarketSide: normalizeMarketSide(order.MarketSide), Settlement: order.ResolvedRouteSettlement(),
		TradeMode:                order.TradeMode,
		RoutingFeeAllowanceSat:   routingFeeLimit(order),
		LightningLiquiditySource: order.LightningLiquiditySource,
	}
	report, err := tf.svc.PreTradeCheck(ctx, preTrade)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: pre-trade check error: %w", err)
	}
	// Even Bisq needs the whitelisted LND address for the replacement CEX
	// withdrawal. Its DEX funding destination is the Bisq wallet, but the new
	// spot BTC must still return to our LND pool.
	if passed, failed := preTradePassed(report, order.ResolvedRouteSettlement()); !passed {
		return nil, fmt.Errorf("tradeflow: pre-trade checks failed: %s", strings.Join(failed, "; "))
	}
	if strings.TrimSpace(report.LNDWithdrawAddress) == "" {
		return nil, fmt.Errorf("tradeflow: hedgeless pre-trade check returned no whitelisted LND address")
	}
	if strings.TrimSpace(order.PaymentMethod) == "" {
		order.PaymentMethod = strings.TrimSpace(report.ResolvedPaymentMethod)
		preTrade.PaymentMethod = order.PaymentMethod
	}

	poolTarget, err := hc.LNDPoolBalanceBTC(ctx, order.Dex)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: capture LND pool target: %w", err)
	}
	if poolTarget+1e-12 < order.RequiredBTCLiquidity {
		return nil, fmt.Errorf("tradeflow: LND pool changed after pre-trade check: available %.8f, required %.8f", poolTarget, order.RequiredBTCLiquidity)
	}

	// Persist the reservation before the first wallet call. From this point on,
	// a crash leaves enough state for the worker/operator to see that the pool
	// may be in use instead of allowing another hedgeless trade to start.
	txID, err := createHedgelessRow(tf.store, order, poolTarget, report.LNDWithdrawAddress)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: reserve hedgeless liquidity pool: %w", err)
	}
	if report.SwapRequired {
		reservationID := fmt.Sprintf("trade-%d", txID)
		if tf.liquidity == nil {
			_ = markHedgelessPreparationFailed(tf.store, txID, "submarine swap is required but execution is not configured")
			return nil, fmt.Errorf("tradeflow: submarine swap is required but execution is not configured")
		}
		if err := tf.liquidity.EnsureOutbound(ctx, reservationID, txID, report); err != nil {
			if liquidityPreparationFundsMayHaveMoved(err) {
				_ = markLiquidityPreparationNeedsReview(tf.store, txID, err.Error())
			} else {
				_ = tf.liquidity.Release(context.WithoutCancel(ctx), reservationID)
				_ = markHedgelessPreparationFailed(tf.store, txID, err.Error())
			}
			return nil, fmt.Errorf("tradeflow: prepare RoboSats outbound liquidity: %w", err)
		}
		defer func() { _ = tf.liquidity.Release(context.WithoutCancel(ctx), reservationID) }()
	}

	if order.QuoteID != "" && strings.EqualFold(order.Dex, "robosats") {
		if err := tf.markRoboSatsPrepared(ctx, txID); err != nil {
			return nil, err
		}
		return tf.resumeRoboSatsHedgeless(ctx, txID, order)
	}
	var spot *cexaccount.SpotOrderResult
	var withdrawal *WithdrawalResult
	var current float64
	// This callback runs immediately after LND accepts the on-chain send or
	// Lightning payment. Refill begins without waiting for Bisq confirmations or
	// later DEX settlement, minimizing the time the shared pool stays depleted.
	onPoolSpent := func(funding *DEXFundingResult) error {
		if err := recordHedgelessFundingReceipt(tf.store, txID, funding); err != nil {
			return err
		}
		var err error
		current, err = hc.LNDPoolBalanceBTC(ctx, order.Dex)
		if err != nil {
			return err
		}
		// A just-submitted spend may not be reflected by every LND balance RPC yet.
		// Never buy less than the BTC committed to this trade, even if the observed
		// target-current deficit temporarily appears smaller.
		// The DEX security deposit is expected to return after fiat settlement.
		// Replacing it here would buy that collateral twice. Restore only the
		// non-returning position amount (plus any swap lockup overhead and fees)
		// while retaining the original target as the final completion threshold.
		interimTarget := hedgelessInterimTarget(poolTarget, order.SecurityDepositBTC)
		minimumDeficit := math.Max(order.BTCAmount, order.RequiredBTCLiquidity-order.SecurityDepositBTC)
		if report.SwapRequired && report.ExpectedLockupSat > report.SwapShortfallSat {
			minimumDeficit += float64(report.ExpectedLockupSat+report.EstimatedFundingFeeSat-report.SwapShortfallSat) / 1e8
		}
		deficit := math.Max(interimTarget-current, minimumDeficit)
		if !strings.EqualFold(order.Provider, "coinbase") {
			deficit += order.CEXWithdrawalFeeBTC
		}
		spot, withdrawal, err = tf.startRefill(ctx, txID, order, preTrade, report.LNDWithdrawAddress, deficit, 1)
		return err
	}
	funding, execution, err := tf.fundHedgelessDEX(ctx, txID, order, onPoolSpent)
	if err != nil {
		_ = markHedgelessExecutionNeedsReview(tf.store, txID, err.Error())
		return nil, fmt.Errorf("tradeflow: hedgeless DEX funding needs review (transaction_id=%d): %w", txID, err)
	}
	if err := recordHedgelessDEXTaken(tf.store, txID, order, funding, execution); err != nil {
		// Take and funding both returned receipts; only the durable write failed.
		// Preserve that stronger safety fact even if the previous mutation did not
		// make the execution metadata visible to a subsequent repository read.
		_ = markDexTakenFundingNeedsReview(tf.store, txID, err.Error())
		return nil, fmt.Errorf("tradeflow: persist hedgeless DEX funding (transaction_id=%d): %w", txID, err)
	}

	return &Result{
		TradeMode:     order.TradeMode,
		SpotBuy:       spot,
		Withdrawal:    withdrawal,
		DEXFunding:    funding,
		PoolRefill:    &PoolRefillResult{TargetBTC: poolTarget, CurrentBTC: current, Status: refillWithdrawalInitiated, Attempt: 1},
		TransactionID: txID,
	}, nil
}

func (tf *TradeFlow) fundHedgelessDEX(ctx context.Context, txID uint, order Order, onPoolSpent func(*DEXFundingResult) error) (*DEXFundingResult, map[string]any, error) {
	exchange := strings.ToLower(strings.TrimSpace(order.Dex))
	amountSat := dexaccount.BTCToSats(order.RequiredBTCLiquidity)
	if order.BisqPlan != nil && order.BisqPlan.TotalToPaySats > 0 {
		amountSat = int64(order.BisqPlan.TotalToPaySats)
	}
	if amountSat <= 0 {
		return nil, nil, fmt.Errorf("invalid BTC funding amount")
	}

	if exchange == "bisq" {
		// Bisq differs from the other venues: an offer can only be taken after its
		// integrated wallet is funded. The required ordering is therefore
		// LND send -> observe Bisq balance -> take offer.
		resolver, ok := tf.directResolver.(directBalanceResolver)
		if !ok {
			return nil, nil, fmt.Errorf("bisq funding balance resolver is not configured")
		}
		address, err := resolver.DirectDestination(ctx, exchange)
		if err != nil {
			return nil, nil, err
		}
		if order.BisqPlan != nil && strings.TrimSpace(order.BisqPlan.FundingAddress) != "" {
			address = strings.TrimSpace(order.BisqPlan.FundingAddress)
		}
		baseline, err := resolver.DirectBalanceBTC(ctx, exchange, address)
		if err != nil {
			return nil, nil, fmt.Errorf("read Bisq funding balance: %w", err)
		}
		if err := markHedgelessFundingInitiated(tf.store, txID, address); err != nil {
			return nil, nil, err
		}
		txid, err := tf.hedgelessWallet.SendOnChain(ctx, address, amountSat, hedgelessSatPerVByte)
		if err != nil {
			return nil, nil, fmt.Errorf("LND to Bisq wallet: %w", err)
		}
		funding := &DEXFundingResult{Exchange: exchange, Method: "lnd_to_bisq_wallet", AmountBTC: dexaccount.SatsToBTC(amountSat), TxID: txid, Destination: address, Status: "funded"}
		if err := onPoolSpent(funding); err != nil {
			return nil, nil, fmt.Errorf("start pool refill after Bisq wallet funding: %w", err)
		}
		if err := waitForBisqFunding(ctx, resolver, exchange, address, baseline+dexaccount.SatsToBTC(amountSat)); err != nil {
			return nil, nil, err
		}
		node, protocol := mostroOfferTransport(order.SelectedOffer)
		taken, err := tf.hedgelessDEX.Take(ctx, dexaccount.OfferInput{
			Exchange:           exchange,
			OfferID:            order.OfferID,
			BTCAmount:          order.BTCAmount,
			FiatAmount:         order.FiatAmount,
			RequestID:          uint64(txID),
			VenueNodePubKey:    node,
			ProtocolVersion:    protocol,
			PaymentAccountID:   hedgelessPaymentAccountID(order),
			MaxWalletDebitSats: hedgelessMaxWalletDebit(order),
			MinVolumeAsLong:    hedgelessMinVolume(order),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("take Bisq offer after wallet funding: %w", err)
		}
		return funding, taken.Execution, nil
	}

	// HodlHodl, RoboSats, and Bisq Easy create their deposit destination while
	// taking the offer, so their ordering is take offer -> resolve address/invoice -> fund.
	taken, err := tf.hedgelessDEX.Take(ctx, robosatsOfferInput(order, txID))
	if err != nil {
		return nil, nil, fmt.Errorf("take %s offer: %w", exchange, err)
	}
	// Save the venue receipt before moving wallet funds. If funding fails, the
	// operator can still identify the already-taken DEX position.
	if err := persistHedgelessExecution(tf.store, txID, taken.Execution); err != nil {
		return nil, nil, err
	}
	if taken.Execution["robosats_funded"] == true {
		escrowBTC := math.Max(0, order.RequiredBTCLiquidity-order.SecurityDepositBTC)
		if v, ok := taken.Execution["amount_sat"].(int64); ok && v > 0 {
			escrowBTC = float64(v) / 1e8
		}
		if err := markHedgelessFundingInitiated(tf.store, txID, ""); err != nil {
			return nil, nil, err
		}
		funding := &DEXFundingResult{
			Exchange:    exchange,
			Method:      "lightning",
			AmountBTC:   escrowBTC,
			PaymentHash: dexaccount.StringField(taken.Execution, "payment_hash"),
			Status:      "funded",
		}
		if err := onPoolSpent(funding); err != nil {
			return nil, nil, fmt.Errorf("start pool refill after RoboSats escrow lock: %w", err)
		}
		return funding, taken.Execution, nil
	}
	address, invoice := strings.TrimSpace(taken.DepositAddress), strings.TrimSpace(taken.LNInvoice)
	if address == "" && invoice == "" {
		address, invoice, err = tf.hedgelessDEX.ResolveDeposit(ctx, exchange, taken.Execution)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := markHedgelessFundingInitiated(tf.store, txID, address); err != nil {
		return nil, nil, err
	}
	if address != "" {
		txid, err := tf.hedgelessWallet.SendOnChain(ctx, address, amountSat, hedgelessSatPerVByte)
		if err != nil {
			return nil, nil, err
		}
		funding := &DEXFundingResult{Exchange: exchange, Method: "onchain", AmountBTC: order.RequiredBTCLiquidity, TxID: txid, Destination: address, Status: "funded"}
		if err := onPoolSpent(funding); err != nil {
			return nil, nil, fmt.Errorf("start pool refill after on-chain funding: %w", err)
		}
		if err := reportHedgelessBtcSent(ctx, tf.hedgelessDEX, exchange, taken.Execution, txid); err != nil {
			return nil, nil, err
		}
		return funding, taken.Execution, nil
	}
	if invoice != "" {
		decoder, ok := tf.hedgelessWallet.(invoiceDecoder)
		if !ok {
			_ = markDexTakenFundingNeedsReview(tf.store, txID, "RoboSats invoice decoder is not configured")
			return nil, nil, fmt.Errorf("RoboSats invoice decoder is not configured")
		}
		decoded, decodeErr := decoder.DecodeInvoice(ctx, invoice)
		if decodeErr != nil {
			_ = markDexTakenFundingNeedsReview(tf.store, txID, decodeErr.Error())
			return nil, nil, fmt.Errorf("decode RoboSats invoice: %w", decodeErr)
		}
		if err := validateRoboSatsInvoice(invoice, decoded, amountSat, time.Now()); err != nil {
			_ = markDexTakenFundingNeedsReview(tf.store, txID, err.Error())
			return nil, nil, err
		}
		_, err := tf.hedgelessWallet.PayInvoice(ctx, invoice, routingFeeLimit(order))
		if err != nil {
			return nil, nil, err
		}
		funding := &DEXFundingResult{Exchange: exchange, Method: "lightning", AmountBTC: order.RequiredBTCLiquidity, PaymentHash: decoded.PaymentHash, Status: "funded"}
		if err := onPoolSpent(funding); err != nil {
			return nil, nil, fmt.Errorf("start pool refill after Lightning funding: %w", err)
		}
		if err := reportHedgelessBtcSent(ctx, tf.hedgelessDEX, exchange, taken.Execution, decoded.PaymentHash); err != nil {
			return nil, nil, err
		}
		return funding, taken.Execution, nil
	}
	return nil, nil, fmt.Errorf("%s returned no funding destination", exchange)
}

func reportHedgelessBtcSent(ctx context.Context, dex hedgelessDEX, exchange string, execution map[string]any, proof string) error {
	if db.DexPlatformFromSlug(exchange) != db.DexBisq2 {
		return nil
	}
	reporter, ok := dex.(interface {
		ReportBtcSent(context.Context, string, string, string) error
	})
	if !ok {
		return nil
	}
	tradeID := dexaccount.StringField(execution, "trade_id")
	if tradeID == "" {
		return fmt.Errorf("bisq2: trade_id missing from execution")
	}
	return reporter.ReportBtcSent(ctx, exchange, tradeID, proof)
}

func mostroOfferTransport(raw []byte) (string, int) {
	protocol := 0
	var offer map[string]any
	if json.Unmarshal(raw, &offer) != nil {
		return "", protocol
	}
	node, _ := offer["venue_node_pubkey"].(string)
	if value, ok := offer["protocol_version"].(float64); ok && value > 0 {
		protocol = int(value)
	}
	return strings.TrimSpace(node), protocol
}

func stringFromOfferJSON(raw []byte, field string) string {
	var offer map[string]any
	if json.Unmarshal(raw, &offer) != nil {
		return ""
	}
	s, _ := offer[field].(string)
	return strings.TrimSpace(s)
}

func routingFeeLimit(order Order) int64 {
	if isRoboOrder(order) {
		return order.RoutingFeeAllowanceSat
	}
	if order.RoutingFeeAllowanceSat > 0 {
		return order.RoutingFeeAllowanceSat
	}
	return defaultRoutingFeeLimitSat
}

func hedgelessInterimTarget(poolTarget, securityDepositBTC float64) float64 {
	return math.Max(0, poolTarget-math.Max(0, securityDepositBTC))
}

func validateRoboSatsInvoice(invoice string, decoded *lnd.DecodedInvoice, preparedSat int64, now time.Time) error {
	if decoded == nil {
		return fmt.Errorf("RoboSats invoice decode returned no data")
	}
	lower := strings.ToLower(strings.TrimSpace(invoice))
	if !strings.HasPrefix(lower, "lnbc") {
		return fmt.Errorf("RoboSats invoice network is not Bitcoin mainnet")
	}
	amount, err := strconv.ParseInt(strings.TrimSpace(decoded.NumSatoshis), 10, 64)
	if err != nil || amount <= 0 {
		return fmt.Errorf("RoboSats invoice has invalid amount")
	}
	if amount > preparedSat {
		return fmt.Errorf("RoboSats invoice amount %d sats exceeds prepared balance %d sats", amount, preparedSat)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(decoded.Timestamp), 10, 64)
	if err != nil {
		return fmt.Errorf("RoboSats invoice timestamp is invalid")
	}
	expiry, err := strconv.ParseInt(strings.TrimSpace(decoded.Expiry), 10, 64)
	if err != nil || expiry <= 0 {
		return fmt.Errorf("RoboSats invoice expiry is invalid")
	}
	if !time.Unix(ts, 0).Add(time.Duration(expiry) * time.Second).After(now) {
		return fmt.Errorf("RoboSats invoice is expired")
	}
	if strings.TrimSpace(decoded.Destination) == "" || strings.TrimSpace(decoded.PaymentHash) == "" {
		return fmt.Errorf("RoboSats invoice destination or payment hash is missing")
	}
	return nil
}

func markDexTakenFundingNeedsReview(store transactionStore, id uint, reason string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.SetStatus(db.StatusDexTakenFundingNeedsReview, reason)
		tx.TransactionComplete = false
	})
}

func markHedgelessPreparationFailed(store transactionStore, id uint, reason string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.SetStatus(db.StatusHedgelessPreparationFailed, reason)
		tx.BTCPoolRefillStatus = refillRestored
		tx.TransactionComplete = true
	})
}

func markLiquidityPreparationNeedsReview(store transactionStore, id uint, reason string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.SetStatus(db.StatusLiquidityPreparationNeedsReview, reason)
		tx.BTCPoolRefillStatus = refillNeedsReview
		tx.TransactionComplete = false
	})
}

func markHedgelessExecutionNeedsReview(store transactionStore, id uint, reason string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		meta, _ := db.ParseDexOfferCreation(tx.DexOfferCreation)
		if meta.Execution != nil || tx.Status == db.StatusDexTakenFundingNeedsReview {
			tx.SetStatus(db.StatusDexTakenFundingNeedsReview, reason)
		} else {
			tx.SetStatus(db.StatusHedgelessFundingNeedsReview, reason)
		}
		tx.BTCPoolRefillStatus = refillNeedsReview
		tx.TransactionComplete = false
	})
}

func hedgelessPaymentAccountID(order Order) string {
	if order.BisqPlan == nil {
		return ""
	}
	return order.BisqPlan.PaymentAccountID
}

func hedgelessMaxWalletDebit(order Order) uint64 {
	if order.BisqPlan == nil {
		return 0
	}
	return order.BisqPlan.MaxWalletDebitSats
}

func hedgelessMinVolume(order Order) int64 {
	if order.BisqPlan == nil {
		return 0
	}
	return order.BisqPlan.MinVolumeAsLong
}

func waitForBisqFunding(ctx context.Context, resolver directBalanceResolver, exchange, address string, wantBTC float64) error {
	// Compare against baseline+amount rather than the absolute amount. A reused
	// Bisq address may already hold BTC, which must not be mistaken for this send.
	deadline := time.NewTimer(bisqFundingPollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(bisqFundingPollInterval)
	defer ticker.Stop()
	for {
		balance, err := resolver.DirectBalanceBTC(ctx, exchange, address)
		if err == nil && balance+1e-12 >= wantBTC {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("bisq wallet funding did not reach %.8f BTC before timeout", wantBTC)
		case <-ticker.C:
		}
	}
}

func (tf *TradeFlow) startRefill(ctx context.Context, txID uint, order Order, preTrade pretrade.Params, lndAddress string, amountBTC float64, attempt int) (*cexaccount.SpotOrderResult, *WithdrawalResult, error) {
	hc := tf.svc.(hedgelessCEX)
	netWithdrawalBTC := amountBTC
	if strings.EqualFold(order.Provider, "coinbase") {
		amountBTC += order.CEXWithdrawalFeeBTC
	}
	entry := refillAttempt{Attempt: attempt, RequestedBTC: amountBTC, CreatedAt: time.Now().UTC()}
	// Persist intent before the market order. Market buys have no portable
	// idempotency guarantee, so a timeout in this state is routed to manual review
	// rather than retried and potentially doubled.
	if err := appendRefillAttempt(tf.store, txID, lndAddress, entry, refillSpotBuyPending); err != nil {
		return nil, nil, err
	}
	spot, err := hc.MarketBuyBTCForRefill(ctx, amountBTC, preTrade)
	if err != nil {
		return nil, nil, err
	}
	if isRoboOrder(order) && (spot == nil || spot.Execution == nil || spot.Execution.FilledBTC <= 0) {
		return nil, nil, fmt.Errorf("RoboSats refill buy has no confirmed fill; reconcile with CEX")
	}
	filled := amountBTC
	if spot.Execution != nil && spot.Execution.FilledBTC > 0 {
		// Withdraw what actually filled, not what was requested. A partial fill is
		// reconciled against the pool target after the withdrawal confirms.
		filled = spot.Execution.FilledBTC
	}
	entry.Spot = spot
	if err := replaceLastRefillAttempt(tf.store, txID, lndAddress, entry, refillSpotBought); err != nil {
		return nil, nil, err
	}
	if strings.EqualFold(order.Provider, "coinbase") && filled+1e-8 < amountBTC {
		return spot, nil, fmt.Errorf("tradeflow: Coinbase refill %.8f BTC is below net target plus withdrawal reserve %.8f BTC", filled, amountBTC)
	}
	// The stable per-attempt id lets recovery distinguish a lost withdrawal
	// response from a withdrawal that was never submitted.
	clientID := fmt.Sprintf("pool-refill-%d-%d", txID, attempt)
	withdrawAmountBTC := filled
	if strings.EqualFold(order.Provider, "coinbase") {
		withdrawAmountBTC = netWithdrawalBTC
	}
	wd, err := tf.svc.WithdrawSpotToLND(ctx, order.Provider, withdrawAmountBTC, lndAddress, clientID)
	if err != nil {
		return spot, nil, err
	}
	withdrawal := &WithdrawalResult{Provider: order.Provider, WithdrawalID: wd.WithdrawalID, ClientRequestID: wd.ClientRequestID, WalletAddress: wd.WalletAddress, AmountBTC: withdrawAmountBTC, CexStatus: "submitted"}
	entry.Withdrawal = withdrawal
	if err := replaceLastRefillAttempt(tf.store, txID, lndAddress, entry, refillWithdrawalInitiated); err != nil {
		return spot, withdrawal, err
	}
	return spot, withdrawal, nil
}

func createHedgelessRow(store transactionStore, order Order, poolTarget float64, lndAddress string) (uint, error) {
	// Keep the complete order snapshot: the background worker must be able to
	// resume without relying on request memory or recalculating historical costs.
	selected := order.SelectedOffer
	if len(selected) == 0 {
		selected, _ = json.Marshal(map[string]any{"offer_id": order.OfferID, "exchange": order.Dex})
	}
	snapshot, err := json.Marshal(order)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	meta, err := db.MarshalDexOfferCreation(db.DexOfferMeta{
		SelectedOffer: selected, LNDReceiveAddress: lndAddress, TradeflowAt: &now,
		TradeflowOrder: snapshot, BisqExecution: order.BisqPlan,
	})
	if err != nil {
		return 0, err
	}
	tx := &db.Transaction{
		Direction: "bid", FiatAmount: order.FiatAmount, FiatCurrency: strings.ToUpper(strings.TrimSpace(order.FiatCurrency)),
		PaymentMethod: order.PaymentMethod, Cex: db.CexExchangeFromProvider(order.Provider), Dex: db.DexPlatformFromSlug(order.Dex),
		Status: db.StatusHedgelessTradeStarted, TradeMode: order.TradeMode, DexOfferID: order.OfferID,
		DexOfferCreation: meta, HubAddress: lndAddress, BTCPoolTargetBTC: poolTarget,
		BTCRequiredLiquidity: order.RequiredBTCLiquidity, BTCPoolRefillStatus: refillPending,
	}
	if tx.FiatCurrency == "" {
		tx.FiatCurrency = "EUR"
	}
	if order.TransactionID > 0 && strings.EqualFold(order.Dex, "robosats") {
		existing, err := store.GetTransaction(order.TransactionID)
		if err != nil {
			return 0, err
		}
		if order.QuoteID == "" || existing.Status != db.StatusTransactionNeedsReview || existing.Dex != db.DexRoboSats || existing.DexOfferID != order.OfferID {
			return 0, fmt.Errorf("RoboSats transaction is not awaiting initial preparation")
		}
		tx.ID, tx.CreatedAt = existing.ID, existing.CreatedAt
		return tx.ID, store.UpdateTransaction(tx)
	}
	if order.TransactionID > 0 {
		existing, err := store.GetTransaction(order.TransactionID)
		if err != nil {
			return 0, err
		}
		if hasFinancialReceipt(existing) {
			return existing.ID, nil
		}
		tx.ID = existing.ID
		tx.CreatedAt = existing.CreatedAt
		if err := store.UpdateTransaction(tx); err != nil {
			return 0, err
		}
		return tx.ID, nil
	}
	if err := store.CreateTransaction(tx); err != nil {
		return 0, err
	}
	return tx.ID, nil
}

func persistHedgelessExecution(store transactionStore, id uint, execution map[string]any) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		meta, _ := db.ParseDexOfferCreation(tx.DexOfferCreation)
		meta.Execution = execution
		tx.DexOfferCreation, _ = db.MarshalDexOfferCreation(meta)
	})
}

func markHedgelessFundingInitiated(store transactionStore, id uint, destination string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.SetStatus(db.StatusHedgelessFundingInitiated, "")
		if strings.TrimSpace(destination) != "" {
			tx.DestinationAddress = strings.TrimSpace(destination)
		}
	})
}

func recordHedgelessFundingReceipt(store transactionStore, id uint, funding *DEXFundingResult) error {
	if funding == nil {
		return fmt.Errorf("hedgeless funding receipt is required")
	}
	relay, err := json.Marshal(db.CexToDexRelayDetails{
		TxID: funding.TxID, PaymentHash: funding.PaymentHash,
		AmountSat: dexaccount.BTCToSats(funding.AmountBTC), DepositAddress: funding.Destination,
		PaymentMethod: funding.Method, SentAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return mutateRow(store, id, func(tx *db.Transaction) { tx.CexToDexWithdrawalTxDetails = relay })
}

func recordHedgelessDEXTaken(store transactionStore, id uint, order Order, funding *DEXFundingResult, execution map[string]any) error {
	relay, err := json.Marshal(db.CexToDexRelayDetails{TxID: funding.TxID, PaymentHash: funding.PaymentHash, AmountSat: dexaccount.BTCToSats(funding.AmountBTC), DepositAddress: funding.Destination, PaymentMethod: funding.Method, SentAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	return mutateRow(store, id, func(tx *db.Transaction) {
		meta, _ := db.ParseDexOfferCreation(tx.DexOfferCreation)
		meta.Execution = execution
		if meta.BisqExecution != nil {
			meta.BisqExecution.Stage = db.BisqStageTaken
		}
		tx.DexOfferCreation, _ = db.MarshalDexOfferCreation(meta)
		tx.CexToDexWithdrawalTxDetails = relay
		tx.CexBitcoinAmount = order.BTCAmount
		if v, ok := execution["fiat_amount"].(float64); ok && v > 0 {
			tx.FiatAmount = v
		} else if order.BisqPlan != nil && order.BisqPlan.FiatAmount > 0 {
			tx.FiatAmount = order.BisqPlan.FiatAmount
		}
		tx.SetStatus(db.StatusDexPositionTaken, "")
		tx.DexOfferTaken = time.Now().UTC()
	})
}

func markHedgelessNeedsReview(store transactionStore, id uint, funding bool) error {
	// funding=false means only the refill is ambiguous; the DEX trade status can
	// continue through its normal settlement lifecycle. funding=true also parks
	// the trade because it is unclear whether the initial LND commitment succeeded.
	return mutateRow(store, id, func(tx *db.Transaction) {
		if funding {
			tx.SetStatus(db.StatusHedgelessFundingNeedsReview, "funding outcome remained ambiguous after the recovery grace period")
		}
		tx.BTCPoolRefillStatus = refillNeedsReview
	})
}

func decodeRefillDetails(raw json.RawMessage) refillDetails {
	// Missing details decode as an empty structure so older/pending rows can still
	// be handled. Callers decide whether emptiness is recoverable for their state.
	var details refillDetails
	_ = json.Unmarshal(raw, &details)
	return details
}

func markHedgelessPoolRestored(store transactionStore, id uint) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.BTCPoolRefillStatus = refillRestored
		details := decodeRefillDetails(tx.BTCPoolRefillDetails)
		if details.SecurityDepositReturn != nil {
			details.SecurityDepositReturn.Status = "returned"
			details.SecurityDepositReturn.LastError = ""
			details.SecurityDepositReturn.UpdatedAt = time.Now().UTC()
			tx.BTCPoolRefillDetails, _ = json.Marshal(details)
		}
	})
}

func appendRefillAttempt(store transactionStore, id uint, address string, attempt refillAttempt, status string) error {
	return mutateRow(store, id, func(tx *db.Transaction) {
		details := decodeRefillDetails(tx.BTCPoolRefillDetails)
		details.LNDAddress = address
		details.Attempts = append(details.Attempts, attempt)
		tx.BTCPoolRefillDetails, _ = json.Marshal(details)
		tx.BTCPoolRefillStatus = status
	})
}

func replaceLastRefillAttempt(store transactionStore, id uint, address string, attempt refillAttempt, status string) error {
	// All receipt and status changes are written together. This prevents recovery
	// from seeing (for example) withdrawal_initiated without its withdrawal ID.
	return mutateRow(store, id, func(tx *db.Transaction) {
		details := decodeRefillDetails(tx.BTCPoolRefillDetails)
		details.LNDAddress = address
		if len(details.Attempts) == 0 {
			details.Attempts = append(details.Attempts, attempt)
		} else {
			details.Attempts[len(details.Attempts)-1] = attempt
		}
		tx.BTCPoolRefillDetails, _ = json.Marshal(details)
		tx.BTCPoolRefillStatus = status
	})
}

// RunRefillWorker monitors the one active hedgeless refill. It confirms the
// recorded LND target and performs at most one corrective buy/withdrawal after
// the previous withdrawal is confirmed on-chain.
func (tf *TradeFlow) RunRefillWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		tf.runRefillOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (tf *TradeFlow) runRefillOnce(ctx context.Context) {
	rows, ok := tf.store.(activeRefillStore)
	if !ok {
		return
	}
	active, err := rows.GetActiveHedgelessRefills()
	if err != nil {
		return
	}
	// The unique database reservation normally yields one row. Iterating the
	// slice also lets deployments recover safely while the index migration is
	// being rolled out or repaired.
	for i := range active {
		if db.IsDurableRoboSats(&active[i]) {
			continue
		}
		if err := tf.advanceRefill(ctx, &active[i]); err != nil {
			slog.Error("tradeflow: advance hedgeless pool refill", "transaction_id", active[i].ID, "err", err)
		}
	}
}

func (tf *TradeFlow) advanceRefill(ctx context.Context, tx *db.Transaction) error {
	if db.BisqManaged(tx) {
		return nil
	}
	hc, ok := tf.svc.(hedgelessCEX)
	if !ok || tx == nil {
		return nil
	}
	switch tx.Status {
	case db.StatusHedgelessTradeStarted:
		// A crash here may straddle liquidity preparation. Conservatively retain
		// the reservation until its durable swap state is reconciled.
		if time.Since(tx.UpdatedAt) >= refillRecoveryGrace {
			return markLiquidityPreparationNeedsReview(tf.store, tx.ID, "liquidity preparation outcome remained ambiguous after the recovery grace period")
		}
		return nil
	case db.StatusHedgelessFundingInitiated:
		// This state straddles the initial DEX/LND funding side effect and must
		// never be recovered by automatically issuing a second payment.
		if time.Since(tx.UpdatedAt) >= refillRecoveryGrace {
			return markHedgelessExecutionNeedsReview(tf.store, tx.ID, "DEX funding outcome remained ambiguous after the recovery grace period")
		}
		return nil
	case db.StatusLiquidityPreparationNeedsReview, db.StatusHedgelessFundingNeedsReview, db.StatusDexTakenFundingNeedsReview:
		return nil
	}
	if tx.BTCPoolRefillStatus == refillNeedsReview {
		return nil
	}
	rc, err := loadRecoveryContext(tx)
	if err != nil {
		return markHedgelessNeedsReview(tf.store, tx.ID, false)
	}
	securityDepositBTC := math.Max(0, rc.order.SecurityDepositBTC)
	interimTarget := hedgelessInterimTarget(tx.BTCPoolTargetBTC, securityDepositBTC)
	current, err := hc.LNDPoolBalanceBTC(ctx, rc.order.Dex)
	if err != nil {
		return err
	}
	details := decodeRefillDetails(tx.BTCPoolRefillDetails)
	refillTarget := interimTarget
	depositReturnConfirmed := false
	returned := details.SecurityDepositReturn
	if returned != nil && !strings.EqualFold(rc.order.Dex, "robosats") && strings.TrimSpace(returned.TxID) != "" && tf.chainSource != nil {
		confirmations, confirmErr := tf.chainSource.TransactionConfirmations(ctx, returned.TxID)
		if confirmErr != nil {
			return confirmErr
		}
		if db.WithdrawalConfirmationsMet(confirmations) {
			depositReturnConfirmed = true
			refillTarget = tx.BTCPoolTargetBTC
		}
	}
	depositReturnObserved := securityDepositBTC <= 1e-12 || depositReturnConfirmed ||
		(strings.EqualFold(rc.order.Dex, "robosats") && returned != nil && (returned.Status == "initiated" || returned.Status == "returned"))
	if depositReturnObserved && current+1e-8 >= tx.BTCPoolTargetBTC {
		return markHedgelessPoolRestored(tf.store, tx.ID)
	}
	if tx.BTCPoolRefillStatus == refillAwaitingDeposit {
		if returned != nil && returned.Status == refillNeedsReview {
			return markHedgelessNeedsReview(tf.store, tx.ID, false)
		}
		// RoboSats cancels the fidelity-bond hold payment back into this same
		// LND wallet. Its aggregate pool balance is the authoritative receipt;
		// do not issue a compensating buy while that asynchronous unlock is pending.
		if strings.EqualFold(rc.order.Dex, "robosats") {
			return nil
		}
		// Bisq returns the deposit with a separate on-chain withdrawal. Only
		// correct a miner-fee shortfall after that return transaction is confirmed.
		if !depositReturnConfirmed {
			return nil
		}
		if len(details.Attempts) >= maxRefillAttempts {
			return markHedgelessNeedsReview(tf.store, tx.ID, false)
		}
		deficit := refillTarget - current
		_, _, err = tf.startRefill(ctx, tx.ID, rc.order, pretrade.Params{
			Provider: rc.order.Provider, FiatCurrency: rc.order.FiatCurrency, RequiredCapital: rc.order.RequiredCapital,
			TradeMode: rc.order.TradeMode, Dex: rc.order.Dex, PaymentMethod: rc.order.PaymentMethod,
		}, details.LNDAddress, deficit, len(details.Attempts)+1)
		return err
	}
	if tx.BTCPoolRefillStatus == refillSpotBuyPending {
		// Unlike withdrawals, the supported spot market-buy APIs cannot be
		// reconciled by our stable client id. Do not retry an unknown outcome.
		if time.Since(tx.UpdatedAt) >= refillRecoveryGrace {
			return markHedgelessNeedsReview(tf.store, tx.ID, false)
		}
		return nil
	}

	if tx.BTCPoolRefillStatus == refillPending {
		// pending can survive a crash after DEX funding was recorded but before
		// startRefill wrote its spot-buy intent. Only start it once the DEX status
		// proves that spending the pool progressed far enough.
		if tx.Status != db.StatusDexPositionTaken && tx.Status != db.StatusDexFiatHasArrived && tx.Status != db.StatusDexPositionReleased {
			return nil
		}
		deficit := refillTarget - current
		if db.IsDurableRoboSats(tx) {
			if tf.robo == nil {
				return fmt.Errorf("RoboSats funding receipt unavailable")
			}
			session, err := tf.robo.Progress(ctx, uint64(tx.ID))
			if err != nil {
				return err
			}
			if session == nil || !session.Funded || session.Payments["escrow"] == nil {
				return fmt.Errorf("RoboSats escrow is not confirmed")
			}
			// LND balance RPCs may lag the hold-payment commitment. Replace
			// the non-returning escrow once, never the refundable bond itself.
			minimum := float64(session.Payments["escrow"].AmountSat+session.Request.BondFeeSat+session.Request.EscrowFeeSat) / 1e8
			deficit = math.Max(deficit, minimum)
			if !strings.EqualFold(rc.order.Provider, "coinbase") {
				deficit += rc.order.CEXWithdrawalFeeBTC
			}
		}
		if deficit <= 1e-8 {
			if securityDepositBTC > 0 {
				return mutateRow(tf.store, tx.ID, func(row *db.Transaction) { row.BTCPoolRefillStatus = refillAwaitingDeposit })
			}
			return markHedgelessPoolRestored(tf.store, tx.ID)
		}
		_, _, err := tf.startRefill(ctx, tx.ID, rc.order, pretrade.Params{
			Provider: rc.order.Provider, FiatCurrency: rc.order.FiatCurrency, RequiredCapital: rc.order.RequiredCapital,
			TradeMode: rc.order.TradeMode, Dex: rc.order.Dex, PaymentMethod: rc.order.PaymentMethod,
		}, details.LNDAddress, deficit, 1)
		return err
	}
	if len(details.Attempts) == 0 {
		return markHedgelessNeedsReview(tf.store, tx.ID, false)
	}
	last := details.Attempts[len(details.Attempts)-1]

	if tx.BTCPoolRefillStatus == refillSpotBought {
		// The process may have died between CEX withdrawal submission and receipt
		// persistence. First search by the stable id; submit only when the CEX can
		// prove that no matching withdrawal exists.
		if time.Since(tx.UpdatedAt) < refillRecoveryGrace || last.Spot == nil {
			return nil
		}
		filled := last.RequestedBTC
		if last.Spot.Execution != nil && last.Spot.Execution.FilledBTC > 0 {
			filled = last.Spot.Execution.FilledBTC
		}
		withdrawAmountBTC := filled
		if strings.EqualFold(rc.order.Provider, "coinbase") {
			withdrawAmountBTC = last.RequestedBTC - rc.order.CEXWithdrawalFeeBTC
			if withdrawAmountBTC <= 0 || filled+1e-8 < last.RequestedBTC {
				return markHedgelessNeedsReview(tf.store, tx.ID, false)
			}
		}
		clientID := fmt.Sprintf("pool-refill-%d-%d", tx.ID, last.Attempt)
		existing, err := tf.svc.WithdrawalAlreadySent(ctx, rc.order.Provider, clientID, withdrawAmountBTC, details.LNDAddress, withdrawalLookback)
		if err != nil {
			return err
		}
		if existing != nil {
			last.Withdrawal = &WithdrawalResult{Provider: rc.order.Provider, WithdrawalID: existing.WithdrawalID, ClientRequestID: clientID, WalletAddress: details.LNDAddress, AmountBTC: withdrawAmountBTC, CexStatus: existing.CexStatus}
			return replaceLastRefillAttempt(tf.store, tx.ID, details.LNDAddress, last, refillWithdrawalInitiated)
		}
		if db.IsDurableRoboSats(tx) {
			// Absence from a possibly delayed history is not proof that an
			// interrupted withdrawal was never accepted by the exchange.
			return markHedgelessNeedsReview(tf.store, tx.ID, false)
		}
		wd, err := tf.svc.WithdrawSpotToLND(ctx, rc.order.Provider, withdrawAmountBTC, details.LNDAddress, clientID)
		if err != nil {
			return err
		}
		last.Withdrawal = &WithdrawalResult{Provider: rc.order.Provider, WithdrawalID: wd.WithdrawalID, ClientRequestID: wd.ClientRequestID, WalletAddress: wd.WalletAddress, AmountBTC: withdrawAmountBTC, CexStatus: "submitted"}
		return replaceLastRefillAttempt(tf.store, tx.ID, details.LNDAddress, last, refillWithdrawalInitiated)
	}

	if tx.BTCPoolRefillStatus != refillWithdrawalInitiated || last.Withdrawal == nil {
		return nil
	}
	statusSvc, ok := tf.svc.(withdrawalStatusService)
	if !ok {
		return nil
	}
	status, err := statusSvc.GetWithdrawalStatus(ctx, rc.order.Provider, last.Withdrawal.WithdrawalID, last.Withdrawal.ClientRequestID)
	if err != nil || status == nil {
		return err
	}
	switch strings.ToLower(status.CexStatus) {
	case "failed", "cancelled", "rejected":
		return markHedgelessNeedsReview(tf.store, tx.ID, false)
	case "completed", "settled":
	default:
		return nil
	}
	if tf.chainSource == nil || strings.TrimSpace(status.TxHash) == "" {
		// A CEX "completed" state alone is insufficient: wait until the Bitcoin
		// transaction is independently visible with the required confirmations.
		return nil
	}
	confirmations, err := tf.chainSource.TransactionConfirmations(ctx, status.TxHash)
	if err != nil || !db.WithdrawalConfirmationsMet(confirmations) {
		return err
	}
	current, err = hc.LNDPoolBalanceBTC(ctx, rc.order.Dex)
	if err != nil {
		return err
	}
	if current+1e-8 >= refillTarget {
		if securityDepositBTC > 0 && !depositReturnConfirmed {
			return mutateRow(tf.store, tx.ID, func(row *db.Transaction) {
				row.BTCPoolRefillStatus = refillAwaitingDeposit
			})
		}
		return markHedgelessPoolRestored(tf.store, tx.ID)
	}
	if len(details.Attempts) >= maxRefillAttempts {
		return markHedgelessNeedsReview(tf.store, tx.ID, false)
	}
	// Fees or a partial fill can leave the pool slightly below its recorded
	// target. Buy only the measured shortfall, and allow at most one correction.
	deficit := refillTarget - current
	_, _, err = tf.startRefill(ctx, tx.ID, rc.order, pretrade.Params{
		Provider: rc.order.Provider, FiatCurrency: rc.order.FiatCurrency, RequiredCapital: rc.order.RequiredCapital,
		TradeMode: rc.order.TradeMode, Dex: rc.order.Dex, PaymentMethod: rc.order.PaymentMethod,
	}, details.LNDAddress, deficit, len(details.Attempts)+1)
	return err
}

// ReconcilePoolRefill clears a needs-review pool reservation only after
// independently reading LND and confirming the recorded target is restored. It
// deliberately does not rewrite a HedgelessFundingNeedsReview trade status: the
// operator may still need to reconcile the DEX position even when the BTC pool
// itself is full again.
func (tf *TradeFlow) ReconcilePoolRefill(ctx context.Context, transactionID uint) (*db.Transaction, error) {
	if tf.store == nil {
		return nil, fmt.Errorf("tradeflow: transaction store is not configured")
	}
	tx, err := tf.store.GetTransaction(transactionID)
	if err != nil {
		return nil, err
	}
	if tx.TradeMode != cexaccount.TradeModeHedgelessDoubleCollateral {
		return nil, fmt.Errorf("tradeflow: transaction %d is not hedgeless", transactionID)
	}
	if tx.BTCPoolRefillStatus != refillNeedsReview {
		return nil, fmt.Errorf("tradeflow: transaction %d is not awaiting pool reconciliation", transactionID)
	}
	rc, err := loadRecoveryContext(tx)
	if err != nil {
		return nil, err
	}
	hc, ok := tf.svc.(hedgelessCEX)
	if !ok {
		return nil, fmt.Errorf("tradeflow: LND pool balance service is not configured")
	}
	current, err := hc.LNDPoolBalanceBTC(ctx, rc.order.Dex)
	if err != nil {
		return nil, err
	}
	if current+1e-8 < tx.BTCPoolTargetBTC {
		return nil, fmt.Errorf("tradeflow: LND pool is still below target: current %.8f, target %.8f", current, tx.BTCPoolTargetBTC)
	}
	if err := markHedgelessPoolRestored(tf.store, tx.ID); err != nil {
		return nil, err
	}
	return tf.store.GetTransaction(tx.ID)
}
