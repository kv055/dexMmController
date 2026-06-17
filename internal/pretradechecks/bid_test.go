package pretradechecks

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// newTestChecker builds a BidChecker whose sources are driven by the given closures.
func newTestChecker(
	balance func() (float64, error),
	whitelist func() ([]string, error),
	perms func() (map[string]string, error),
	walletAddrs func() ([]string, error),
) *BidChecker {
	c := NewBidChecker(
		BalanceFetcherFunc(func(context.Context, string, string) (float64, error) { return balance() }),
		WhitelistFetcherFunc(func(context.Context, string) ([]string, error) { return whitelist() }),
		PermissionCheckerFunc(func(context.Context, string) (map[string]string, error) { return perms() }),
	)
	if walletAddrs != nil {
		c.SetWallet(WalletSourceFunc(func(context.Context) ([]string, error) { return walletAddrs() }))
	}
	return c
}

func okPerms() (map[string]string, error) {
	return map[string]string{permSpotTrading: permGranted, permHedgePositions: permGranted}, nil
}

func TestRunAllChecksPass(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500)
	if !report.OK {
		t.Fatalf("expected OK, got %+v", report)
	}
	if len(report.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(report.Checks))
	}
	for _, chk := range report.Checks {
		if !chk.Passed {
			t.Errorf("check %q unexpectedly failed: %s", chk.Name, chk.Message)
		}
	}
	if report.LNDWithdrawAddress != "bc1qwalletaddr" {
		t.Errorf("LNDWithdrawAddress: want bc1qwalletaddr, got %q", report.LNDWithdrawAddress)
	}
}

// TestRunRunsToCompletion is the key behavioural guarantee: even when the FIRST check
// (permissions) fails, the later checks (capital, whitelist) are still evaluated and
// reported — the runner does not short-circuit.
func TestRunRunsToCompletion(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 100, nil },                    // capital: 100 < 500 → fail
		func() ([]string, error) { return []string{"bc1qother"}, nil }, // whitelist: no wallet match → fail
		func() (map[string]string, error) {
			return map[string]string{permSpotTrading: "denied"}, nil // permissions → fail
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500)
	if report.OK {
		t.Fatal("expected report.OK=false")
	}
	if len(report.Checks) != 3 {
		t.Fatalf("expected all 3 checks reported, got %d", len(report.Checks))
	}
	byName := map[string]CheckResult{}
	for _, chk := range report.Checks {
		byName[chk.Name] = chk
	}
	for _, name := range []string{"permissions", "capital", "whitelist"} {
		chk, ok := byName[name]
		if !ok {
			t.Fatalf("check %q missing from report", name)
		}
		if chk.Passed {
			t.Errorf("check %q expected to fail but passed", name)
		}
		if chk.Message == "" {
			t.Errorf("check %q failed without a message", name)
		}
	}
}

func TestRunPartialFailureReportsBoth(t *testing.T) {
	// Permissions + capital pass, whitelist fails. The passing checks must still report.
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qother"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500)
	if report.OK {
		t.Fatal("expected OK=false due to whitelist")
	}
	passed := map[string]bool{}
	for _, chk := range report.Checks {
		passed[chk.Name] = chk.Passed
	}
	if !passed["permissions"] || !passed["capital"] {
		t.Errorf("expected permissions+capital to pass, got %+v", passed)
	}
	if passed["whitelist"] {
		t.Error("expected whitelist to fail")
	}
	if report.LNDWithdrawAddress != "" {
		t.Errorf("LNDWithdrawAddress should be empty when whitelist fails, got %q", report.LNDWithdrawAddress)
	}
}

func TestRunAllAggregatesAllFailures(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 100, nil },
		func() ([]string, error) { return []string{"bc1qother"}, nil },
		func() (map[string]string, error) { return map[string]string{permSpotTrading: "denied"}, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
	)
	err := c.RunAll(context.Background(), "kraken", "EUR", 500)
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	// The aggregated error should mention every failure (semicolon-joined).
	if strings.Count(err.Error(), ";") < 2 {
		t.Errorf("expected aggregated message of 3 failures, got: %v", err)
	}
}

func TestCheckEvaluationErrorReportedAsFailure(t *testing.T) {
	// An infrastructure error fetching balance must surface as a failed capital check,
	// not panic or be silently ignored.
	c := newTestChecker(
		func() (float64, error) { return 0, fmt.Errorf("exchange unreachable") },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500)
	if report.OK {
		t.Fatal("expected OK=false")
	}
	for _, chk := range report.Checks {
		if chk.Name == "capital" {
			if chk.Passed || !strings.Contains(chk.Message, "unreachable") {
				t.Errorf("capital check should report the fetch error, got %+v", chk)
			}
		}
	}
}
