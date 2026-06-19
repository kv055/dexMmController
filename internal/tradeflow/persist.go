package tradeflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dexMmController/internal/db"
)

// transactionStore persists mm transaction rows after tradeflow part 1.
type transactionStore interface {
	GetTransaction(id uint) (*db.Transaction, error)
	CreateTransaction(tx *db.Transaction) error
	UpdateTransaction(tx *db.Transaction) error
}

// PersistBidResult creates or updates a transaction row after a successful bid tradeflow.
// Status is set to CexCryptoWithdrawalInitiated; DexOfferCreation stores offer linkage and withdrawal metadata.
func PersistBidResult(ctx context.Context, repo transactionStore, order Order, result *Result, lndAddr string) (uint, error) {
	if repo == nil {
		return 0, fmt.Errorf("transaction repository is required")
	}
	if result == nil {
		return 0, fmt.Errorf("tradeflow result is required")
	}
	offerID := strings.TrimSpace(order.OfferID)
	dexSlug := strings.TrimSpace(order.Dex)
	if offerID == "" || dexSlug == "" {
		return 0, fmt.Errorf("offer_id and dex are required to persist bid tradeflow")
	}

	now := time.Now().UTC()
	selectedOffer := order.SelectedOffer
	if len(selectedOffer) == 0 {
		selectedOffer, _ = json.Marshal(map[string]any{
			"offer_id": offerID,
			"exchange": dexSlug,
		})
	}

	meta := db.DexOfferMeta{
		SelectedOffer:     selectedOffer,
		LNDReceiveAddress: strings.TrimSpace(lndAddr),
		CexWithdrawal: &db.CexWithdrawalMeta{
			WithdrawalID:    result.Withdrawal.WithdrawalID,
			ClientRequestID: result.Withdrawal.ClientRequestID,
			Provider:        result.Withdrawal.Provider,
		},
		TradeflowAt: &now,
	}
	dexOfferJSON, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return 0, err
	}

	cexDetails, err := json.Marshal(map[string]any{
		"spot":       result.SpotBuy,
		"hedge":      result.Hedge,
		"withdrawal": result.Withdrawal,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal cex transaction details: %w", err)
	}

	fiatAmount := order.FiatAmount
	if fiatAmount <= 0 && result.SpotBuy != nil && result.SpotBuy.Execution != nil {
		fiatAmount = result.SpotBuy.Execution.FilledFiat
	}
	fiatCurrency := strings.TrimSpace(order.FiatCurrency)
	if fiatCurrency == "" {
		fiatCurrency = "EUR"
	}

	if lndAddr = strings.TrimSpace(lndAddr); lndAddr == "" {
		lndAddr = strings.TrimSpace(result.Withdrawal.WalletAddress)
	}

	tx := &db.Transaction{
		Direction:               "BUY",
		FiatAmount:              fiatAmount,
		FiatCurrency:            fiatCurrency,
		Cex:                     db.CexExchangeFromProvider(order.Provider),
		CexHedgingPosition:      db.CexExchangeFromProvider(hedgeVenueToCex(order.HedgeVenue)),
		Dex:                     db.DexPlatformFromSlug(dexSlug),
		Status:                  db.StatusCexCryptoWithdrawalInitiated,
		CexTransactionDetails:   cexDetails,
		CexBitcoinAmount:        result.Withdrawal.AmountBTC,
		CexTransactionTimestamp: now,
		DexOfferCreation:        dexOfferJSON,
		DexBtcAddress:           lndAddr,
	}

	if order.TransactionID > 0 {
		existing, err := repo.GetTransaction(order.TransactionID)
		if err != nil {
			return 0, err
		}
		tx.ID = existing.ID
		tx.CreatedAt = existing.CreatedAt
		if err := repo.UpdateTransaction(tx); err != nil {
			return 0, err
		}
		return tx.ID, nil
	}

	if err := repo.CreateTransaction(tx); err != nil {
		return 0, err
	}
	return tx.ID, nil
}

func hedgeVenueToCex(venue string) string {
	venue = strings.TrimSpace(venue)
	if idx := strings.Index(venue, "_"); idx > 0 {
		return venue[:idx]
	}
	return venue
}
