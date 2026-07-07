package tradeflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/obs"
	"dexMmController/internal/txroute"
)

// withdrawalLookback bounds how far back the withdrawal history-match guard looks
// when deciding whether a recovery retry would double-withdraw.
const withdrawalLookback = 7 * 24 * time.Hour

// cexDetailsBundle mirrors what bundleCexDetails writes into CexTransactionDetails.
type cexDetailsBundle struct {
	Spot       *cexaccount.SpotOrderResult `json:"spot"`
	Hedge      *cexaccount.HedgeOpenResult `json:"hedge"`
	Withdrawal *WithdrawalResult           `json:"withdrawal"`
}

// recoveryContext is the rebuilt view of a stuck row: its original order plus the
// step results recorded so far.
type recoveryContext struct {
	order   Order
	bundle  cexDetailsBundle
	lndAddr string
}

// loadRecoveryContext reconstructs the order and prior-step results from a row so a
// recovery entrypoint can resume it without the original request.
func loadRecoveryContext(tx *db.Transaction) (*recoveryContext, error) {
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return nil, fmt.Errorf("parse DexOfferCreation: %w", err)
	}
	if len(meta.TradeflowOrder) == 0 {
		return nil, fmt.Errorf("row has no tradeflow order snapshot; cannot recover")
	}
	var order Order
	if err := json.Unmarshal(meta.TradeflowOrder, &order); err != nil {
		return nil, fmt.Errorf("unmarshal tradeflow order snapshot: %w", err)
	}

	var bundle cexDetailsBundle
	if len(tx.CexTransactionDetails) > 0 {
		if err := json.Unmarshal(tx.CexTransactionDetails, &bundle); err != nil {
			return nil, fmt.Errorf("unmarshal cex transaction details: %w", err)
		}
	}

	// The withdrawal target: the LND hub on hub/swap routes, or the direct
	// destination (e.g. our bisq wallet) on the direct route.
	lndAddr := strings.TrimSpace(meta.LNDReceiveAddress)
	if lndAddr == "" {
		lndAddr = strings.TrimSpace(tx.HubAddress)
	}
	if lndAddr == "" {
		lndAddr = strings.TrimSpace(tx.DestinationAddress)
	}
	return &recoveryContext{order: order, bundle: bundle, lndAddr: lndAddr}, nil
}

// ResumeFromTradeflowStarted recovers a row stuck at CexTradeflowStarted. If the spot
// fill was already recorded (so the buy definitely happened), it resumes from the
// hedge step. If the spot outcome is unknown it cannot safely re-buy, so it parks the
// row at CexTradeflowNeedsReview for a human. It never calls MarketBuyBTC.
func (tf *TradeFlow) ResumeFromTradeflowStarted(ctx context.Context, tx *db.Transaction) (err error) {
	defer func() { obs.TradeflowRecoveries.WithLabelValues("tradeflow_started", obs.Result(err)).Inc() }()
	if tf.store == nil {
		return fmt.Errorf("tradeflow: recovery requires a transaction store")
	}
	rc, err := loadRecoveryContext(tx)
	if err != nil {
		return err
	}

	if rc.bundle.Spot == nil {
		slog.Error("tradeflow recovery: spot-buy outcome unknown — parking for manual review",
			"transaction_id", tx.ID, "provider", rc.order.Provider)
		return mutateRow(tf.store, tx.ID, func(t *db.Transaction) {
			t.Status = db.StatusCexTradeflowNeedsReview
		})
	}

	// Spot is confirmed. Open the hedge (guarded against double-open) and advance.
	hedge := rc.bundle.Hedge
	if hedge == nil {
		hedge, err = tf.openHedgeWithRetry(ctx, rc.order)
		if err != nil {
			slog.Error("tradeflow recovery: hedge still failing — parking at HedgeFailedNeedsReview",
				"transaction_id", tx.ID, "hedge_venue", rc.order.HedgeVenue, "err", err)
			return markHedgeFailed(tf.store, tx.ID, rc.bundle.Spot)
		}
	}
	if err := recordHedge(tf.store, tx.ID, rc.order, rc.bundle.Spot, hedge); err != nil {
		return fmt.Errorf("tradeflow recovery: persist hedge: %w", err)
	}
	slog.Info("tradeflow recovery: resumed from CexTradeflowStarted, hedge open",
		"transaction_id", tx.ID, "venue", hedge.Venue)
	return nil
}

// RecoverHedge re-checks the venue for a row stuck at HedgeFailedNeedsReview: a hedge
// that errored during Run may have actually opened (delayed fill / lost confirmation).
// If the position is now present at the expected side and size, it records the hedge
// and advances to CexTradeSuccessful. Otherwise it leaves the row for manual review.
func (tf *TradeFlow) RecoverHedge(ctx context.Context, tx *db.Transaction) (err error) {
	defer func() { obs.TradeflowRecoveries.WithLabelValues("hedge", obs.Result(err)).Inc() }()
	if tf.store == nil {
		return fmt.Errorf("tradeflow: recovery requires a transaction store")
	}
	rc, err := loadRecoveryContext(tx)
	if err != nil {
		return err
	}
	if rc.bundle.Spot == nil {
		return fmt.Errorf("tradeflow recovery: HedgeFailedNeedsReview row has no recorded spot fill")
	}

	side := strings.ToLower(strings.TrimSpace(rc.order.HedgeSide))
	snap, err := tf.svc.LiveHedgePosition(ctx, rc.order.HedgeVenue)
	if err != nil {
		return fmt.Errorf("tradeflow recovery: live hedge position check: %w", err)
	}
	if snap == nil || snap.Side != side || snap.SizeBTC < rc.order.HedgeNotionalBTC*hedgeOpenedFraction {
		// Still unhedged — nothing safe to do automatically; leave for manual review.
		return nil
	}

	hedge := &cexaccount.HedgeOpenResult{
		Venue:       rc.order.HedgeVenue,
		Side:        side,
		BTCNotional: snap.SizeBTC,
		Status:      "open_detected_during_recovery",
	}
	if err := recordHedge(tf.store, tx.ID, rc.order, rc.bundle.Spot, hedge); err != nil {
		return fmt.Errorf("tradeflow recovery: persist recovered hedge: %w", err)
	}
	slog.Warn("tradeflow recovery: hedge found open on venue during review — advancing to CexTradeSuccessful",
		"transaction_id", tx.ID, "venue", rc.order.HedgeVenue, "size_btc", snap.SizeBTC)
	return nil
}

// ResumeWithdrawal recovers a row stuck at CexTradeSuccessful (spot + hedge done,
// withdrawal not sent). It first checks the exchange's withdrawal history so a retry
// can't double-withdraw on venues without an idempotency key; if a matching
// withdrawal already exists it records that and advances, otherwise it issues the
// withdrawal with the stable per-transaction key.
func (tf *TradeFlow) ResumeWithdrawal(ctx context.Context, tx *db.Transaction) (err error) {
	defer func() { obs.TradeflowRecoveries.WithLabelValues("withdrawal", obs.Result(err)).Inc() }()
	if tf.store == nil {
		return fmt.Errorf("tradeflow: recovery requires a transaction store")
	}
	rc, err := loadRecoveryContext(tx)
	if err != nil {
		return err
	}
	if rc.bundle.Spot == nil || rc.bundle.Hedge == nil {
		return fmt.Errorf("tradeflow recovery: CexTradeSuccessful row missing spot/hedge details")
	}
	if rc.lndAddr == "" {
		return fmt.Errorf("tradeflow recovery: no LND withdrawal address on row %d", tx.ID)
	}
	method := txroute.BidMethod(rc.order.Dex)

	filledBTC := tx.CexBitcoinAmount
	if filledBTC <= 0 && rc.bundle.Spot.Execution != nil {
		filledBTC = rc.bundle.Spot.Execution.FilledBTC
	}
	if filledBTC <= 0 {
		return fmt.Errorf("tradeflow recovery: cannot determine filled BTC for row %d", tx.ID)
	}
	clientReqID := fmt.Sprintf("tradeflow-%d", tx.ID)

	// Guard: has a matching withdrawal already gone out?
	if existing, err := tf.svc.WithdrawalAlreadySent(ctx, rc.order.Provider, clientReqID, filledBTC, rc.lndAddr, withdrawalLookback); err != nil {
		slog.Warn("tradeflow recovery: withdrawal history check failed; not issuing to avoid double-send",
			"transaction_id", tx.ID, "err", err)
		return err
	} else if existing != nil {
		slog.Warn("tradeflow recovery: withdrawal already exists on venue — recording, not resending",
			"transaction_id", tx.ID, "withdrawal_id", existing.WithdrawalID)
		result := withdrawalResultFrom(rc.order.Provider, existing, clientReqID, rc.lndAddr, filledBTC)
		return recordWithdrawal(tf.store, tx.ID, rc.order, rc.bundle.Spot, rc.bundle.Hedge, result, rc.lndAddr, method)
	}

	wd, err := tf.svc.WithdrawSpotToLND(ctx, rc.order.Provider, filledBTC, rc.lndAddr, clientReqID)
	if err != nil {
		return fmt.Errorf("tradeflow recovery: withdrawal failed: %w", err)
	}
	slog.Info("tradeflow recovery: withdrawal issued",
		"transaction_id", tx.ID, "withdrawal_id", wd.WithdrawalID)
	result := &Result{
		SpotBuy: rc.bundle.Spot,
		Hedge:   rc.bundle.Hedge,
		Withdrawal: WithdrawalResult{
			Provider:        rc.order.Provider,
			WithdrawalID:    wd.WithdrawalID,
			ClientRequestID: wd.ClientRequestID,
			WalletAddress:   wd.WalletAddress,
			AmountBTC:       filledBTC,
			CexStatus:       "submitted",
		},
		TransactionID: tx.ID,
	}
	return recordWithdrawal(tf.store, tx.ID, rc.order, rc.bundle.Spot, rc.bundle.Hedge, result, rc.lndAddr, method)
}

// withdrawalResultFrom builds a Result around an already-existing withdrawal found by
// the history-match guard.
func withdrawalResultFrom(provider string, status *cexaccount.WithdrawalStatus, clientReqID, lndAddr string, filledBTC float64) *Result {
	addr := strings.TrimSpace(status.Address)
	if addr == "" {
		addr = lndAddr
	}
	return &Result{
		Withdrawal: WithdrawalResult{
			Provider:        provider,
			WithdrawalID:    status.WithdrawalID,
			ClientRequestID: clientReqID,
			WalletAddress:   addr,
			AmountBTC:       filledBTC,
			CexStatus:       status.CexStatus,
		},
	}
}
