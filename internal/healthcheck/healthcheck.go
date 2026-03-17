package healthcheck

import (
	"context"
	"fmt"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/openclaw"
)

// HealthChecker queries the DB for incomplete transactions and verifies their
// actual state against external systems, advancing the status where appropriate.
type HealthChecker struct {
	repo     *db.TransactionRepository
	openclaw *openclaw.Client
}

func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client) *HealthChecker {
	return &HealthChecker{repo: repo, openclaw: oc}
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

// verifyFiatToCexInitiated checks whether the fiat transfer has already arrived
// at the CEX. If so, the status is advanced to FiatToCexArrived.
//
// TODO: query the CEX specified in tx.Cex for the incoming fiat balance /
// deposit history to confirm arrival.
func (h *HealthChecker) verifyFiatToCexInitiated(ctx context.Context, tx *db.Transaction) error {
	// TODO: call CEX API (tx.Cex) to check for fiat deposit arrival
	// TODO: if no money at CEX, call openclaw instance and ask if fiat has been sent to CEX
	return nil
}

// verifyFiatToCexArrived confirms the fiat is present at the CEX and that a
// spot trade has not already been executed. Advances to CexTradeSuccessful if
// the trade is already done.
//
// TODO: query CEX trade/order history for this transaction.
func (h *HealthChecker) verifyFiatToCexArrived(ctx context.Context, tx *db.Transaction) error {
	// TODO: call CEX API (tx.Cex) to check for fiat deposit arrival

	return nil
}

// verifyCexTradeSuccessful reads the CEX transaction history to confirm the
// fiat-to-BTC conversion is recorded. Advances to CexCryptoWithdrawalInitiated
// if a withdrawal has already been triggered.
//
// TODO: query CEX trade history for completed BTC buy order.
func (h *HealthChecker) verifyCexTradeSuccessful(ctx context.Context, tx *db.Transaction) error {
	// TODO: verify CexTransactionDetails reflects a completed trade
	return nil
}

// verifyCexCryptoWithdrawalInitiated checks whether the on-chain withdrawal
// transaction has been broadcast and confirmed. Advances to
// CexCryptoWithdrawalArrived on confirmation.
//
// TODO: look up the tx hash from CexToDexWithdrawalTxDetails on-chain.
func (h *HealthChecker) verifyCexCryptoWithdrawalInitiated(ctx context.Context, tx *db.Transaction) error {
	// TODO: check on-chain confirmation count for withdrawal tx hash
	return nil
}

// verifyCexCryptoWithdrawalArrived confirms the BTC is spendable in the DEX
// wallet and that a DEX offer has not already been placed. Advances to
// DexPositionOpen if an offer already exists.
//
// TODO: query DEX wallet balance / UTXOs.
func (h *HealthChecker) verifyCexCryptoWithdrawalArrived(ctx context.Context, tx *db.Transaction) error {
	// TODO: check DEX wallet (tx.Dex) for confirmed incoming UTXO
	return nil
}

// verifyDexPositionOpen checks the DEX (tx.Dex) to confirm the offer is still
// live. Advances to DexPositionTaken if a counterparty has already matched.
//
// TODO: connect to Bisq / HodlHodl / RoboSats API and fetch offer status.
func (h *HealthChecker) verifyDexPositionOpen(ctx context.Context, tx *db.Transaction) error {
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

// verifyDexPositionReleased connects to the DEX specified in tx.Dex and checks
// whether the BTC has actually been released to the buyer.
//
// TODO: implement per-DEX release confirmation (Bisq, HodlHodl, RoboSats).
func (h *HealthChecker) verifyDexPositionReleased(ctx context.Context, tx *db.Transaction) error {
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
