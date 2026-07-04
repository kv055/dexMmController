package cexaccount

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakePaymentSetup is an in-memory payment_methods catalog plus the rows the
// methods link to, mirroring the DB-backed fetcher's (nil, nil) not-found
// semantics.
type fakePaymentSetup struct {
	methods  map[string]*PaymentMethodSnapshot
	accounts map[int64]*FiatAccountSnapshot
	banks    map[int64]*BankAccountSnapshot
	wallets  map[int64]*WalletAddressSnapshot
}

func (f *fakePaymentSetup) PaymentMethodByName(_ context.Context, name string) (*PaymentMethodSnapshot, error) {
	return f.methods[name], nil
}

func (f *fakePaymentSetup) PickPaymentMethod(_ context.Context, names []string) (*PaymentMethodSnapshot, error) {
	var best *PaymentMethodSnapshot
	for _, n := range names {
		row := f.methods[n]
		if row == nil || !row.IsActive || !row.IsLinked {
			continue
		}
		if best == nil || row.Priority > best.Priority ||
			(row.Priority == best.Priority && row.Name < best.Name) {
			best = row
		}
	}
	return best, nil
}

func (f *fakePaymentSetup) FiatAccountByID(_ context.Context, id int64) (*FiatAccountSnapshot, error) {
	return f.accounts[id], nil
}

func (f *fakePaymentSetup) BankAccountByID(_ context.Context, id int64) (*BankAccountSnapshot, error) {
	return f.banks[id], nil
}

func (f *fakePaymentSetup) WalletAddressByID(_ context.Context, id int64) (*WalletAddressSnapshot, error) {
	return f.wallets[id], nil
}

func ptr(v int64) *int64 { return &v }

// okPaymentSetup returns a catalog where Revolut and SEPA are linked+active
// and valid.
func okPaymentSetup() *fakePaymentSetup {
	return &fakePaymentSetup{
		methods: map[string]*PaymentMethodSnapshot{
			"Revolut": {Name: "Revolut", Category: "Fiat Transmitter", AccountID: ptr(1), IsActive: true, IsLinked: true, Priority: 5},
			"SEPA":    {Name: "SEPA", Category: "Bank Transfer", BankAccountID: ptr(1), IsActive: true, IsLinked: true, Priority: 1},
		},
		accounts: map[int64]*FiatAccountSnapshot{
			1: {Provider: "Revolut", Email: "user@example.com", Phone: "@revtag"},
		},
		banks: map[int64]*BankAccountSnapshot{
			1: {Scheme: "iban_bic", Details: map[string]string{"iban": "DE89370400440532013000"}, IsActive: true},
		},
		wallets: map[int64]*WalletAddressSnapshot{},
	}
}

func newTestChecker(
	balance func() (float64, error),
	whitelist func() ([]string, error),
	perms func() (map[string]string, error),
	walletAddrs func() ([]string, error),
	paymentSetup PaymentSetupFetcher,
) *BidChecker {
	c := NewBidChecker(
		BalanceFetcherFunc(func(context.Context, string, string) (float64, error) { return balance() }),
		WhitelistFetcherFunc(func(context.Context, string) ([]string, error) { return whitelist() }),
		PermissionCheckerFunc(func(context.Context, string) (map[string]string, error) { return perms() }),
		paymentSetup,
	)
	if walletAddrs != nil {
		c.SetWallet(WalletSourceFunc(func(context.Context) ([]string, error) { return walletAddrs() }))
	}
	return c
}

func okPerms() (map[string]string, error) {
	return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermGranted}, nil
}

// paymentCheck extracts the payment_setup result from a report.
func paymentCheck(t *testing.T, report Report) CheckResult {
	t.Helper()
	for _, chk := range report.Checks {
		if chk.Name == "payment_setup" {
			return chk
		}
	}
	t.Fatal("payment_setup check missing from report")
	return CheckResult{}
}

func TestRunAllChecksPass(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500, "Revolut", nil)
	if !report.OK {
		t.Fatalf("expected OK, got %+v", report)
	}
	if len(report.Checks) != 4 {
		t.Fatalf("expected 4 checks, got %d", len(report.Checks))
	}
	for _, chk := range report.Checks {
		if !chk.Passed {
			t.Errorf("check %q unexpectedly failed: %s", chk.Name, chk.Message)
		}
	}
	if report.LNDWithdrawAddress != "bc1qwalletaddr" {
		t.Errorf("LNDWithdrawAddress: want bc1qwalletaddr, got %q", report.LNDWithdrawAddress)
	}
	if report.ResolvedPaymentMethod != "Revolut" {
		t.Errorf("ResolvedPaymentMethod: want Revolut, got %q", report.ResolvedPaymentMethod)
	}
}

func TestRunRunsToCompletion(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 100, nil },
		func() ([]string, error) { return []string{"bc1qother"}, nil },
		func() (map[string]string, error) {
			return map[string]string{PermSpotTrading: "denied"}, nil
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500, "Revolut", nil)
	if report.OK {
		t.Fatal("expected report.OK=false")
	}
	if len(report.Checks) != 4 {
		t.Fatalf("expected all 4 checks reported, got %d", len(report.Checks))
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
	if chk := byName["payment_setup"]; !chk.Passed {
		t.Errorf("payment_setup should pass with configured account, got %+v", chk)
	}
}

func TestRunPartialFailureReportsBoth(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qother"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500, "Revolut", nil)
	if report.OK {
		t.Fatal("expected OK=false due to whitelist")
	}
	passed := map[string]bool{}
	for _, chk := range report.Checks {
		passed[chk.Name] = chk.Passed
	}
	if !passed["permissions"] || !passed["capital"] || !passed["payment_setup"] {
		t.Errorf("expected permissions+capital+payment_setup to pass, got %+v", passed)
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
		func() (map[string]string, error) { return map[string]string{PermSpotTrading: "denied"}, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	err := c.RunAll(context.Background(), "kraken", "EUR", 500, "Revolut", nil)
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if strings.Count(err.Error(), ";") < 2 {
		t.Errorf("expected aggregated message of multiple failures, got: %v", err)
	}
}

func TestCheckEvaluationErrorReportedAsFailure(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 0, fmt.Errorf("exchange unreachable") },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500, "Revolut", nil)
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

// runPaymentCheck runs the checker with all non-payment checks passing.
func runPaymentCheck(t *testing.T, setup PaymentSetupFetcher, method string, methods []string) (Report, CheckResult) {
	t.Helper()
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		setup,
	)
	report := c.Run(context.Background(), "kraken", "EUR", 500, method, methods)
	return report, paymentCheck(t, report)
}

func TestPaymentSetupUnknownMethod(t *testing.T) {
	_, chk := runPaymentCheck(t, okPaymentSetup(), "Frobnicate Pay", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not in the payment methods catalog") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupInactiveMethod(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Revolut"].IsActive = false
	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed || !strings.Contains(chk.Message, "disabled") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupUnlinkedTransmitter(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Revolut"].AccountID = nil
	setup.methods["Revolut"].IsLinked = false
	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not linked to a Fiat Transmitter account") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupLinkedAccountGone(t *testing.T) {
	setup := okPaymentSetup()
	delete(setup.accounts, 1)
	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed || !strings.Contains(chk.Message, "no longer exists") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupMissingRevolutTag(t *testing.T) {
	setup := okPaymentSetup()
	setup.accounts[1].Phone = ""
	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed || !strings.Contains(chk.Message, "Revolut tag") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupUnlinkedBankRail(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["SEPA"].BankAccountID = nil
	setup.methods["SEPA"].IsLinked = false
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not linked to a bank account") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountInactive(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[1].IsActive = false
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "inactive") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountWrongScheme(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[1].Scheme = "sort_code"
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "expected iban_bic") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountMissingIBAN(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[1].Details = map[string]string{}
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "no IBAN") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupCryptoLinked(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Lightning"] = &PaymentMethodSnapshot{
		Name: "Lightning", Category: "Crypto", WalletAddressID: ptr(7), IsActive: true, IsLinked: true,
	}
	setup.wallets[7] = &WalletAddressSnapshot{Label: "Primary", Address: "bc1qwalletaddr"}
	_, chk := runPaymentCheck(t, setup, "Lightning", nil)
	if !chk.Passed {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupOthersUnsupported(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Mystery"] = &PaymentMethodSnapshot{
		Name: "Mystery", Category: "Others", IsActive: true,
	}
	_, chk := runPaymentCheck(t, setup, "Mystery", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not supported for automated trading") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupRequiresMethod(t *testing.T) {
	_, chk := runPaymentCheck(t, okPaymentSetup(), "", nil)
	if chk.Passed || !strings.Contains(chk.Message, "payment method is required") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupPicksHighestPriority(t *testing.T) {
	setup := okPaymentSetup() // Revolut priority 5, SEPA priority 1, both eligible
	report, chk := runPaymentCheck(t, setup, "", []string{"SEPA", "Revolut"})
	if !chk.Passed {
		t.Fatalf("payment_setup check = %+v", chk)
	}
	if report.ResolvedPaymentMethod != "Revolut" {
		t.Errorf("ResolvedPaymentMethod: want Revolut (priority 5 > 1), got %q", report.ResolvedPaymentMethod)
	}
}

func TestPaymentSetupPickSkipsUnlinked(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Revolut"].IsLinked = false // higher priority but ineligible
	report, chk := runPaymentCheck(t, setup, "", []string{"SEPA", "Revolut"})
	if !chk.Passed {
		t.Fatalf("payment_setup check = %+v", chk)
	}
	if report.ResolvedPaymentMethod != "SEPA" {
		t.Errorf("ResolvedPaymentMethod: want SEPA, got %q", report.ResolvedPaymentMethod)
	}
}

func TestPaymentSetupPickNoneEligible(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Revolut"].IsActive = false
	setup.methods["SEPA"].IsLinked = false
	_, chk := runPaymentCheck(t, setup, "", []string{"SEPA", "Revolut"})
	if chk.Passed || !strings.Contains(chk.Message, "none of the offer's payment methods") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}
