package tradeflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
)

// transactionStore persists mm transaction rows as the tradeflow progresses.
// Each irreversible CEX step is recorded immediately so a half-finished cycle is
// recoverable from the database rather than living only in an in-flight request.
type transactionStore interface {
	GetTransaction(id uint) (*db.Transaction, error)
	CreateTransaction(tx *db.Transaction) error
	UpdateTransaction(tx *db.Transaction) error
}

// createTradeflowRow creates (or reuses, when order.TransactionID is set) the
// transaction row before the spot buy, at StatusCexTradeflowStarted. Returning the
// row ID early gives the rest of the flow a stable idempotency key.
func createTradeflowRow(store transactionStore, order Order, lndAddr string) (uint, error) {
	offerID := strings.TrimSpace(order.OfferID)
	dexSlug := strings.TrimSpace(order.Dex)

	selectedOffer := order.SelectedOffer
	if len(selectedOffer) == 0 {
		selectedOffer, _ = json.Marshal(map[string]any{
			"offer_id": offerID,
			"exchange": dexSlug,
		})
	}
	now := time.Now().UTC()
	// Snapshot the order so the recovery worker can rebuild it from the row alone.
	orderSnapshot, err := json.Marshal(order)
	if err != nil {
		return 0, fmt.Errorf("marshal tradeflow order snapshot: %w", err)
	}
	meta := db.DexOfferMeta{
		SelectedOffer:     selectedOffer,
		LNDReceiveAddress: strings.TrimSpace(lndAddr),
		TradeflowAt:       &now,
		TradeflowOrder:    orderSnapshot,
	}
	dexOfferJSON, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return 0, err
	}

	fiatCurrency := strings.TrimSpace(order.FiatCurrency)
	if fiatCurrency == "" {
		fiatCurrency = "EUR"
	}

	tx := &db.Transaction{
		Direction:        "BUY",
		FiatAmount:       order.FiatAmount,
		FiatCurrency:     fiatCurrency,
		Cex:              db.CexExchangeFromProvider(order.Provider),
		Dex:              db.DexPlatformFromSlug(dexSlug),
		Status:           db.StatusCexTradeflowStarted,
		DexOfferID:       offerID,
		DexOfferCreation: dexOfferJSON,
		DexBtcAddress:    strings.TrimSpace(lndAddr),
	}

	if order.TransactionID > 0 {
		existing, err := store.GetTransaction(order.TransactionID)
		if err != nil {
			return 0, err
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

// mutateRow loads the row, applies mutate, and saves it. Loading first preserves
// columns written by earlier steps (UpdateTransaction saves the whole row).
func mutateRow(store transactionStore, id uint, mutate func(*db.Transaction)) error {
	existing, err := store.GetTransaction(id)
	if err != nil {
		return err
	}
	mutate(existing)
	return store.UpdateTransaction(existing)
}

// recordSpotBuy persists the spot fill immediately after it succeeds.
func recordSpotBuy(store transactionStore, id uint, spot *cexaccount.SpotOrderResult, filledBTC float64) error {
	details, err := bundleCexDetails(spot, nil, nil)
	if err != nil {
		return err
	}
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.CexTransactionDetails = details
		tx.CexBitcoinAmount = filledBTC
		tx.CexTransactionTimestamp = time.Now().UTC()
		tx.CexTransactionFee = spot.FeeInCurrency(tx.FiatCurrency)
	})
}

// recordHedge persists the hedge immediately after it opens, advancing the row to
// StatusCexTradeSuccessful (spot + hedge done, withdrawal not yet sent).
func recordHedge(store transactionStore, id uint, order Order, spot *cexaccount.SpotOrderResult, hedge *cexaccount.HedgeOpenResult) error {
	details, err := bundleCexDetails(spot, hedge, nil)
	if err != nil {
		return err
	}
	hedgeOpen, err := json.Marshal(hedge)
	if err != nil {
		return fmt.Errorf("marshal hedge open details: %w", err)
	}
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.Status = db.StatusCexTradeSuccessful
		tx.CexHedgingPosition = db.CexExchangeFromProvider(hedgeVenueToCex(order.HedgeVenue))
		tx.CexTransactionDetails = details
		tx.CexCounterPositionOpenDetails = hedgeOpen
	})
}

// markHedgeFailed records that the spot filled but the hedge could not be opened or
// confirmed after retries — the account holds unhedged BTC and needs manual review.
func markHedgeFailed(store transactionStore, id uint, spot *cexaccount.SpotOrderResult) error {
	details, err := bundleCexDetails(spot, nil, nil)
	if err != nil {
		return err
	}
	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.Status = db.StatusHedgeFailedNeedsReview
		tx.CexTransactionDetails = details
	})
}

// recordWithdrawal persists the withdrawal as the final tradeflow step, advancing
// the row to StatusCexCryptoWithdrawalInitiated and writing offer linkage so the
// downstream dexrelay worker can take over.
func recordWithdrawal(store transactionStore, id uint, order Order, spot *cexaccount.SpotOrderResult, hedge *cexaccount.HedgeOpenResult, result *Result, lndAddr string) error {
	offerID := strings.TrimSpace(order.OfferID)
	dexSlug := strings.TrimSpace(order.Dex)

	selectedOffer := order.SelectedOffer
	if len(selectedOffer) == 0 {
		selectedOffer, _ = json.Marshal(map[string]any{
			"offer_id": offerID,
			"exchange": dexSlug,
		})
	}
	now := time.Now().UTC()
	orderSnapshot, err := json.Marshal(order)
	if err != nil {
		return fmt.Errorf("marshal tradeflow order snapshot: %w", err)
	}
	meta := db.DexOfferMeta{
		SelectedOffer:     selectedOffer,
		LNDReceiveAddress: strings.TrimSpace(lndAddr),
		CexWithdrawal: &db.CexWithdrawalMeta{
			WithdrawalID:    result.Withdrawal.WithdrawalID,
			ClientRequestID: result.Withdrawal.ClientRequestID,
			Provider:        result.Withdrawal.Provider,
		},
		TradeflowAt:    &now,
		TradeflowOrder: orderSnapshot,
	}
	dexOfferJSON, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return err
	}
	details, err := bundleCexDetails(spot, hedge, &result.Withdrawal)
	if err != nil {
		return err
	}

	addr := strings.TrimSpace(lndAddr)
	if addr == "" {
		addr = strings.TrimSpace(result.Withdrawal.WalletAddress)
	}

	return mutateRow(store, id, func(tx *db.Transaction) {
		tx.Status = db.StatusCexCryptoWithdrawalInitiated
		tx.CexTransactionDetails = details
		tx.CexBitcoinAmount = result.Withdrawal.AmountBTC
		tx.DexOfferCreation = dexOfferJSON
		if tx.DexOfferID == "" {
			tx.DexOfferID = offerID
		}
		if addr != "" {
			tx.DexBtcAddress = addr
		}
	})
}

// bundleCexDetails serializes the spot/hedge/withdrawal outcomes known so far into
// the CexTransactionDetails JSON column. nil legs are omitted.
func bundleCexDetails(spot *cexaccount.SpotOrderResult, hedge *cexaccount.HedgeOpenResult, withdrawal *WithdrawalResult) (json.RawMessage, error) {
	bundle := map[string]any{}
	if spot != nil {
		bundle["spot"] = spot
	}
	if hedge != nil {
		bundle["hedge"] = hedge
	}
	if withdrawal != nil {
		bundle["withdrawal"] = withdrawal
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("marshal cex transaction details: %w", err)
	}
	return raw, nil
}

func hedgeVenueToCex(venue string) string {
	venue = strings.TrimSpace(venue)
	if idx := strings.Index(venue, "_"); idx > 0 {
		return venue[:idx]
	}
	return venue
}
