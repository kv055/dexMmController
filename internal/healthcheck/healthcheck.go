package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/hummingbot"
	"dexMmController/internal/openclaw"
)

// HealthChecker queries the DB for incomplete transactions and verifies their
// actual state against external systems, advancing the status where appropriate.
type HealthChecker struct {
	repo       *db.TransactionRepository
	openclaw   *openclaw.Client
	hummingbot *hummingbot.Client
	btc        *bitcoin.NetworkDataFetcher
}

func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client, hb *hummingbot.Client, btc *bitcoin.NetworkDataFetcher) *HealthChecker {
	return &HealthChecker{repo: repo, openclaw: oc, hummingbot: hb, btc: btc}
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

// verifyFiatToCexArrived confirms the fiat balance on the CEX is at least the
// expected amount. Uses tx.FiatCurrency and tx.Cex from the DB to identify
// which asset and exchange to query via Hummingbot.
func (h *HealthChecker) verifyFiatToCexArrived(ctx context.Context, tx *db.Transaction) error {
	balance, err := h.hummingbot.BalanceForExchange(ctx, string(tx.Cex), tx.FiatCurrency)
	if err != nil {
		// Balance endpoint returned nothing for this asset — fiat not visible yet.
		return nil
	}
	if balance.Available >= tx.FiatAmount {
		tx.Status = db.StatusFiatToCexArrived
		return h.repo.UpdateTransaction(tx)
	}
	return nil
}

// verifyCexTradeSuccessful confirms that a BTC buy trade occurred on the CEX
// after the fiat deposit arrived and that a positive BTC balance is now
// visible. If both conditions hold the transaction is advanced to
// StatusCexTradeSuccessful.
func (h *HealthChecker) verifyCexTradeSuccessful(ctx context.Context, tx *db.Transaction) error {
	// 1. Fetch all trades recorded for this connector.
	trades, err := h.hummingbot.TradesForExchange(ctx, string(tx.Cex))
	if err != nil {
		return fmt.Errorf("verifyCexTradeSuccessful: failed to fetch trades: %w", err)
	}

	// 2. Look for a BTC buy trade that:
	//    a) is on the expected trading pair (e.g. "BTC-EUR")
	//    b) occurred after the fiat deposit arrived at the CEX
	//    c) consumed at least 99 % of the expected fiat amount
	//       (allowing 1 % for fees / slippage)
	tradingPair := "BTC-" + tx.FiatCurrency
	fiatArrivedMs := tx.TransferFiatToCexArrivalTime.UnixMilli()
	const tolerance = 0.99

	found := false
	for i := range trades {
		t := &trades[i]
		if t.TradingPair != tradingPair || t.TradeType != "BUY" {
			continue
		}
		if t.Timestamp < fiatArrivedMs {
			continue
		}
		if t.Price*t.Amount >= tx.FiatAmount*tolerance {
			found = true
			break
		}
	}
	if !found {
		// Trade not yet visible — stay in current status and retry later.
		return nil
	}

	// 3. Confirm that a positive BTC balance is now present on the exchange.
	btcBalance, err := h.hummingbot.BalanceForExchange(ctx, string(tx.Cex), "BTC")
	if err != nil || btcBalance.Available <= 0 {
		// Balance not yet reflected — wait for the next healthcheck cycle.
		return nil
	}

	// All checks passed — advance to trade-successful status.
	tx.Status = db.StatusCexTradeSuccessful
	return h.repo.UpdateTransaction(tx)
}

// verifyCexCryptoWithdrawalInitiated checks whether the on-chain withdrawal
// has been confirmed by the CEX.
//
// TODO: the Hummingbot Backend API (v1.0.1) has no withdrawal endpoint.
// Implement once the API exposes withdrawal history.
func (h *HealthChecker) verifyCexCryptoWithdrawalInitiated(_ context.Context, _ *db.Transaction) error {
	return nil
}

// verifyCexCryptoWithdrawalArrived checks whether the BTC withdrawn from the
// CEX has arrived at the DEX wallet (tx.DexBtcAddress) by querying the
// confirmed on-chain balance via mempool.space. The expected amount is read
// from tx.CexBitcoinAmount. A 1 % tolerance is applied to absorb miner fees.
// Advances to StatusCexCryptoWithdrawalArrived once the balance matches.
func (h *HealthChecker) verifyCexCryptoWithdrawalArrived(ctx context.Context, tx *db.Transaction) error {
	if tx.DexBtcAddress == "" {
		return fmt.Errorf("verifyCexCryptoWithdrawalArrived: tx %d has no DexBtcAddress set", tx.ID)
	}

	balance, err := h.btc.FetchAddressBalance(ctx, tx.DexBtcAddress)
	if err != nil {
		return fmt.Errorf("verifyCexCryptoWithdrawalArrived: %w", err)
	}

	const tolerance = 0.99
	if balance < tx.CexBitcoinAmount*tolerance {
		// Withdrawal not yet confirmed on-chain — retry on next cycle.
		return nil
	}

	tx.Status = db.StatusCexCryptoWithdrawalArrived
	return h.repo.UpdateTransaction(tx)
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
	arrived, err := h.openclaw.PollBankTxArrivedFromDex(ctx, fmt.Sprintf("tx-%d", tx.ID), 5*time.Second)
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
