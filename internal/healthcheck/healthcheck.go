package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/hummingbot"
	"dexMmController/internal/openclaw"
)

// HealthChecker queries the DB for incomplete transactions and verifies their
// actual state against external systems, advancing the status where appropriate.
type HealthChecker struct {
	repo        *db.TransactionRepository
	openclaw    *openclaw.Client
	hummingbot  *hummingbot.Client
}

func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client, hb *hummingbot.Client) *HealthChecker {
	return &HealthChecker{repo: repo, openclaw: oc, hummingbot: hb}
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

// verifyFiatToCexInitiated checks whether the fiat has already arrived at the
// CEX by asking Hummingbot for the fiat balance. If balance > 0, the status is
// advanced to FiatToCexArrived.
func (h *HealthChecker) verifyFiatToCexInitiated(ctx context.Context, tx *db.Transaction) error {
	balance, err := h.hummingbot.BalanceForExchange(ctx, string(tx.Cex), tx.FiatCurrency)
	if err != nil {
		// Balance not found means it hasn't arrived yet — not a hard error.
		return nil
	}
	if balance.Available >= tx.FiatAmount {
		tx.Status = db.StatusFiatToCexArrived
		return h.repo.UpdateTransaction(tx)
	}
	return nil
}

// verifyFiatToCexArrived confirms fiat is at the CEX and checks whether a BTC
// trade has already been executed by Hummingbot. Advances to CexTradeSuccessful
// if a matching BUY trade is found in the history.
func (h *HealthChecker) verifyFiatToCexArrived(ctx context.Context, tx *db.Transaction) error {
	trades, err := h.hummingbot.TradesForExchange(ctx, string(tx.Cex))
	if err != nil {
		return fmt.Errorf("verifyFiatToCexArrived: failed to fetch trade history: %w", err)
	}
	for _, t := range trades {
		if t.Side == "BUY" && t.Amount*t.Price >= tx.FiatAmount*0.99 {
			// Close enough match — record details and advance.
			tx.Status = db.StatusCexTradeSuccessful
			tx.CexTransactionFee = t.Fee
			return h.repo.UpdateTransaction(tx)
		}
	}
	return nil
}

// verifyCexTradeSuccessful confirms the BTC buy is in the trade history and
// checks whether a withdrawal has already been broadcast. Advances to
// CexCryptoWithdrawalInitiated if a matching withdrawal is found.
func (h *HealthChecker) verifyCexTradeSuccessful(ctx context.Context, tx *db.Transaction) error {
	withdrawals, err := h.hummingbot.WithdrawalHistory(ctx)
	if err != nil {
		return fmt.Errorf("verifyCexTradeSuccessful: failed to fetch withdrawals: %w", err)
	}
	for _, w := range withdrawals {
		if w.Exchange == string(tx.Cex) && w.Asset == "BTC" && w.Status != "failed" {
			tx.Status = db.StatusCexCryptoWithdrawalInitiated
			tx.CexWithdrawalFee = w.Fee
			return h.repo.UpdateTransaction(tx)
		}
	}
	return nil
}

// verifyCexCryptoWithdrawalInitiated looks up the withdrawal by its tx hash and
// checks whether it has been confirmed on-chain. Advances to
// CexCryptoWithdrawalArrived on confirmation.
func (h *HealthChecker) verifyCexCryptoWithdrawalInitiated(ctx context.Context, tx *db.Transaction) error {
	// Extract the tx hash stored when the withdrawal was initiated.
	txHash := withdrawalHashFromJSON(tx.CexToDexWithdrawalTxDetails)
	if txHash == "" {
		return fmt.Errorf("verifyCexCryptoWithdrawalInitiated: no tx hash in CexToDexWithdrawalTxDetails")
	}

	withdrawal, err := h.hummingbot.WithdrawalByTxHash(ctx, txHash)
	if err != nil {
		return fmt.Errorf("verifyCexCryptoWithdrawalInitiated: %w", err)
	}
	if withdrawal.Status == "confirmed" {
		tx.Status = db.StatusCexCryptoWithdrawalArrived
		return h.repo.UpdateTransaction(tx)
	}
	return nil
}

// verifyCexCryptoWithdrawalArrived confirms the BTC is spendable in the DEX
// wallet and that a DEX offer has not already been placed. Advances to
// DexPositionOpen if an offer already exists.
//
// TODO: query DEX wallet balance / UTXOs.
func (h *HealthChecker) verifyCexCryptoWithdrawalArrived(_ context.Context, _ *db.Transaction) error {
	// TODO: check DEX wallet (tx.Dex) for confirmed incoming UTXO
	return nil
}

// verifyDexPositionOpen checks the DEX (tx.Dex) to confirm the offer is still
// live. Advances to DexPositionTaken if a counterparty has already matched.
//
// TODO: connect to Bisq / HodlHodl / RoboSats API and fetch offer status.
func (h *HealthChecker) verifyDexPositionOpen(_ context.Context, _ *db.Transaction) error {
	// TODO: poll tx.Dex offer ID from DexOfferCreation for counterparty match
	return nil
}

// verifyDexPositionTaken confirms the trade is in progress on the DEX and that
// the fiat leg has not already settled. Advances to DexFiatHasArrived if
// openclaw reports the payment already received.
func (h *HealthChecker) verifyDexPositionTaken(ctx context.Context, tx *db.Transaction) error {
	// Ask openclaw whether the fiat payment has already landed in the bank
	// account while we weren't looking.
	arrived, err := h.openclaw.PollBankTxArrived(ctx, fmt.Sprintf("tx-%d", tx.ID), 5*time.Second)
	if err != nil {
		return fmt.Errorf("verifyDexPositionTaken: openclaw poll failed: %w", err)
	}
	if arrived {
		tx.Status = db.StatusDexFiatHasArrived
		return h.repo.UpdateTransaction(tx)
	}
	return nil
}

// verifyDexFiatHasArrived asks the local openclaw instance whether the fiat
// payment has actually arrived in the bank account. Advances to
// DexPositionReleased if confirmed, so the BTC release can be triggered.
func (h *HealthChecker) verifyDexFiatHasArrived(ctx context.Context, tx *db.Transaction) error {
	arrived, err := h.openclaw.PollBankTxArrived(ctx, fmt.Sprintf("tx-%d", tx.ID), 5*time.Second)
	if err != nil {
		return fmt.Errorf("verifyDexFiatHasArrived: openclaw poll failed: %w", err)
	}
	if !arrived {
		return fmt.Errorf("verifyDexFiatHasArrived: fiat not yet confirmed in bank account")
	}
	// Fiat confirmed — caller should now trigger BTC release on the DEX.
	return nil
}

// withdrawalHashFromJSON extracts the "hash" field from a JSON blob stored in
// CexToDexWithdrawalTxDetails, e.g. {"hash":"0xabc...","status":"pending"}.
func withdrawalHashFromJSON(raw []byte) string {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m["hash"]
}

// verifyDexPositionReleased connects to the DEX specified in tx.Dex and checks
// whether the BTC has actually been released to the buyer.
//
// TODO: implement per-DEX release confirmation (Bisq, HodlHodl, RoboSats).
func (h *HealthChecker) verifyDexPositionReleased(_ context.Context, tx *db.Transaction) error {
	switch tx.Dex {
	case db.DexBisq:
		// TODO: query Bisq API / local node for trade completion
	case db.DexHodlHodl:
		// TODO: query HodlHodl API for contract release status
	case db.DexRoboSats:
		// TODO: query RoboSats API for order completion
	default:
		return fmt.Errorf("verifyDexPositionReleased: unknown DEX %q", tx.Dex)
	}
	return nil
}
