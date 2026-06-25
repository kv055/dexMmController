package cexaccount

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	Name    string `json:"name"`
	Label   string `json:"label"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Report is the full result of running every pre-trade check. OK is true only when
// every check passed.
type Report struct {
	OK                 bool          `json:"ok"`
	Checks             []CheckResult `json:"checks"`
	LNDWithdrawAddress string        `json:"lnd_withdraw_address,omitempty"`
}

// Run executes every pre-trade check to completion — it never short-circuits on the
// first failure — and returns a per-check Report.
func (c *BidChecker) Run(ctx context.Context, provider, currency string, requiredCapital float64) Report {
	checks := []struct {
		name, label string
		run         func() error
	}{
		{"permissions", "API key permissions", func() error { return c.checkPermissions(ctx, provider) }},
		{"capital", "Sufficient capital", func() error { return c.checkCapital(ctx, provider, currency, requiredCapital) }},
	}

	report := Report{OK: true, Checks: make([]CheckResult, 0, len(checks)+1)}
	for _, chk := range checks {
		res := CheckResult{Name: chk.name, Label: chk.label, Passed: true}
		if err := chk.run(); err != nil {
			res.Passed = false
			res.Message = err.Error()
			report.OK = false
		}
		report.Checks = append(report.Checks, res)
	}

	whitelistRes := CheckResult{Name: "whitelist", Label: "Withdrawal whitelist", Passed: true}
	lndAddr, err := c.checkWhitelist(ctx, provider)
	if err != nil {
		whitelistRes.Passed = false
		whitelistRes.Message = err.Error()
		report.OK = false
	} else {
		report.LNDWithdrawAddress = lndAddr
	}
	report.Checks = append(report.Checks, whitelistRes)

	return report
}

// RunAll runs every check and returns an aggregated error listing all failures,
// or nil when all checks pass.
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

func (c *BidChecker) checkPermissions(ctx context.Context, provider string) error {
	perms, err := c.permissions.CEXPermissions(ctx, provider)
	if err != nil {
		return fmt.Errorf("reading %s API key permissions for pre-trade check: %w", provider, err)
	}
	if len(perms) == 0 {
		return fmt.Errorf("no permission data available for %s; run the account health check first", provider)
	}

	if perms[PermSpotTrading] != PermGranted {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have spot trading permission (status: %q); check account health",
			provider, perms[PermSpotTrading],
		)
	}

	if hedgeStatus := perms[PermHedgePositions]; hedgeStatus != PermGranted && hedgeStatus != PermUnsupported {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have hedge position permission (status: %q); check account health",
			provider, hedgeStatus,
		)
	}

	return nil
}

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

func (c *BidChecker) checkWhitelist(ctx context.Context, provider string) (string, error) {
	c.walletMu.RLock()
	wallet := c.wallet
	c.walletMu.RUnlock()
	if wallet == nil {
		return "", fmt.Errorf("LND wallet not available; cannot verify that %s whitelist contains an LND wallet address", provider)
	}

	cexAddresses, err := c.whitelist.WhitelistedBTCAddresses(ctx, provider)
	if err != nil {
		return "", fmt.Errorf("fetching %s whitelist for pre-trade check: %w", provider, err)
	}

	walletAddresses, err := wallet.OnChainAddresses(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching LND wallet addresses for pre-trade check: %w", err)
	}
	if addr := MatchWhitelistedLNDAddress(walletAddresses, cexAddresses); addr != "" {
		return addr, nil
	}

	return "", fmt.Errorf(
		"pre-trade check failed: %s whitelist contains no address from the current LND wallet; add an LND on-chain address to the %s withdrawal whitelist before trading",
		provider, provider,
	)
}
