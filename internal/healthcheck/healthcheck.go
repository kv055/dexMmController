package healthcheck

import (
	"context"
	"fmt"
	"log"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/dex/hodlhodl"
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
	hodlhodl   *hodlhodl.Client
}

// NewHealthChecker creates a health checker.
func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client, hb *hummingbot.Client, btc *bitcoin.NetworkDataFetcher, hh *hodlhodl.Client) *HealthChecker {
	return &HealthChecker{repo: repo, openclaw: oc, hummingbot: hb, btc: btc, hodlhodl: hh}
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
	case db.DexBisq, db.DexRoboSats:
		// TODO: implement Bisq / RoboSats contract polling
	default:
		return fmt.Errorf("verifyDexPositionOpen: unknown DEX %q", tx.Dex)
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
		paid, err := h.hodlhodl.ContractIsPaid(ctx, contractID)
		if err != nil {
			return fmt.Errorf("verifyDexPositionTaken: %w", err)
		}
		if paid {
			tx.Status = db.StatusDexFiatHasArrived
			return h.repo.UpdateTransaction(tx)
		}
	case db.DexBisq, db.DexRoboSats:
		// TODO: implement Bisq / RoboSats contract status check
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
