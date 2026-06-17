package pretradechecks

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Canonical permission names and status values — mirrors accounthealth constants so
// this package stays self-contained with no import of accounthealth.
const (
	permSpotTrading    = "spot_trading"
	permHedgePositions = "hedge_positions"
	permGranted        = "granted"
	permUnsupported    = "unsupported"
)

// BidChecker runs all pre-trade checks for the bid side (buying BTC on a CEX).
type BidChecker struct {
	balance     BalanceFetcher
	whitelist   WhitelistFetcher
	permissions PermissionChecker
	walletMu    sync.RWMutex
	wallet      WalletSource
}

// NewBidChecker creates a BidChecker. The wallet source is set lazily via SetWallet
// because the LND client initialises asynchronously after startup.
func NewBidChecker(balance BalanceFetcher, whitelist WhitelistFetcher, permissions PermissionChecker) *BidChecker {
	return &BidChecker{balance: balance, whitelist: whitelist, permissions: permissions}
}

// SetWallet injects (or replaces) the wallet source. Safe to call concurrently.
func (c *BidChecker) SetWallet(ws WalletSource) {
	c.walletMu.Lock()
	c.wallet = ws
	c.walletMu.Unlock()
}

// CheckResult is the outcome of a single named pre-trade check.
type CheckResult struct {
	// Name is the stable machine identifier (e.g. "permissions").
	Name string `json:"name"`
	// Label is the human-readable check name (e.g. "API key permissions").
	Label string `json:"label"`
	// Passed is true when the check succeeded.
	Passed bool `json:"passed"`
	// Message carries the failure detail (or evaluation error) when Passed is false.
	Message string `json:"message,omitempty"`
}

// Report is the full result of running every pre-trade check. OK is true only when
// every check passed.
type Report struct {
	OK     bool          `json:"ok"`
	Checks []CheckResult `json:"checks"`
}

// Run executes every pre-trade check to completion — it never short-circuits on the
// first failure — and returns a per-check Report. A check that cannot be evaluated
// (an infrastructure error fetching balances, permissions, etc.) is reported as a
// failed check, since the trade's safety can't be confirmed without it.
func (c *BidChecker) Run(ctx context.Context, provider, currency string, requiredCapital float64) Report {
	checks := []struct {
		name, label string
		run         func() error
	}{
		{"permissions", "API key permissions", func() error { return c.checkPermissions(ctx, provider) }},
		{"capital", "Sufficient capital", func() error { return c.checkCapital(ctx, provider, currency, requiredCapital) }},
		{"whitelist", "Withdrawal whitelist", func() error { return c.checkWhitelist(ctx, provider) }},
	}

	report := Report{OK: true, Checks: make([]CheckResult, 0, len(checks))}
	for _, chk := range checks {
		res := CheckResult{Name: chk.name, Label: chk.label, Passed: true}
		if err := chk.run(); err != nil {
			res.Passed = false
			res.Message = err.Error()
			report.OK = false
		}
		report.Checks = append(report.Checks, res)
	}
	return report
}

// RunAll runs every check to completion (via Run) and returns an aggregated error
// listing all failures, or nil when all checks pass. It is the gate used by the
// actual market-buy path so a buy is never placed while any check is failing.
func (c *BidChecker) RunAll(ctx context.Context, provider, currency string, requiredCapital float64) error {
	report := c.Run(ctx, provider, currency, requiredCapital)
	if report.OK {
		return nil
	}
	msgs := make([]string, 0, len(report.Checks))
	for _, chk := range report.Checks {
		if !chk.Passed {
			msgs = append(msgs, chk.Message)
		}
	}
	return fmt.Errorf("pre-trade checks failed: %s", strings.Join(msgs, "; "))
}

// checkPermissions reads the cached CEX API key permission state from the DB and verifies
// that spot trading is granted and hedge positions are not explicitly denied.
func (c *BidChecker) checkPermissions(ctx context.Context, provider string) error {
	perms, err := c.permissions.CEXPermissions(ctx, provider)
	if err != nil {
		return fmt.Errorf("reading %s API key permissions for pre-trade check: %w", provider, err)
	}
	if len(perms) == 0 {
		return fmt.Errorf("no permission data available for %s; run the account health check first", provider)
	}

	if perms[permSpotTrading] != permGranted {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have spot trading permission (status: %q); check account health",
			provider, perms[permSpotTrading],
		)
	}

	// hedge_positions = "unsupported" is acceptable — Kraken and Coinbase use separate
	// API keys for futures; only an explicit "denied" blocks the trade.
	if hedgeStatus := perms[permHedgePositions]; hedgeStatus != permGranted && hedgeStatus != permUnsupported {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have hedge position permission (status: %q); check account health",
			provider, hedgeStatus,
		)
	}

	return nil
}

// checkCapital verifies the provider account holds enough fiat to cover the total capital
// requirement (spot notional + hedge collateral + security deposit).
func (c *BidChecker) checkCapital(ctx context.Context, provider, currency string, required float64) error {
	available, err := c.balance.FiatAvailable(ctx, provider, currency)
	if err != nil {
		return fmt.Errorf("checking account balance before market buy: %w", err)
	}
	fiat := strings.ToUpper(strings.TrimSpace(currency))
	if fiat == "" {
		fiat = "EUR"
	}
	if available < required {
		return fmt.Errorf(
			"insufficient %s balance on %s: available %.2f, capital requirement %.2f (spot buy + hedge collateral + security deposit)",
			fiat, provider, available, required,
		)
	}
	return nil
}

// checkWhitelist verifies the provider's whitelisted withdrawal addresses contain at
// least one address from the current LND wallet, ensuring funds can be returned after
// the trade.
func (c *BidChecker) checkWhitelist(ctx context.Context, provider string) error {
	c.walletMu.RLock()
	wallet := c.wallet
	c.walletMu.RUnlock()
	if wallet == nil {
		return fmt.Errorf("LND wallet not available; cannot verify that %s whitelist contains an LND wallet address", provider)
	}

	cexAddresses, err := c.whitelist.WhitelistedBTCAddresses(ctx, provider)
	if err != nil {
		return fmt.Errorf("fetching %s whitelist for pre-trade check: %w", provider, err)
	}
	cexSet := make(map[string]struct{}, len(cexAddresses))
	for _, addr := range cexAddresses {
		cexSet[strings.ToLower(addr)] = struct{}{}
	}

	walletAddresses, err := wallet.OnChainAddresses(ctx)
	if err != nil {
		return fmt.Errorf("fetching LND wallet addresses for pre-trade check: %w", err)
	}
	for _, addr := range walletAddresses {
		if _, ok := cexSet[strings.ToLower(addr)]; ok {
			return nil
		}
	}

	return fmt.Errorf(
		"pre-trade check failed: %s whitelist contains no address from the current LND wallet; add an LND on-chain address to the %s withdrawal whitelist before trading",
		provider, provider,
	)
}
