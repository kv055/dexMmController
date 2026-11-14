package cexaccount

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"commons/paymentmethods"
	"dexTakerController/internal/lsp"
	"dexTakerController/internal/pretrade"
)

// fakePaymentSetup is an in-memory payment_methods catalog plus the rows the
// methods link to, mirroring the DB-backed fetcher's (nil, nil) not-found
// semantics.
type fakePaymentSetup struct {
	methods   map[string]*PaymentMethodSnapshot
	aliases   map[string][]string // canonical name -> operator aliases
	accounts  map[int64]*FiatAccountSnapshot
	banks     map[int64]*BankAccountSnapshot
	methodErr error
}

func (f *fakePaymentSetup) findMethod(name string) *PaymentMethodSnapshot {
	for _, row := range f.methods {
		if row != nil && paymentmethods.CatalogLabelMatches(row.Name, f.aliases[row.Name], name) {
			return row
		}
	}
	return nil
}

func (f *fakePaymentSetup) PaymentMethodByName(_ context.Context, name string) (*PaymentMethodSnapshot, error) {
	if f.methodErr != nil {
		return nil, f.methodErr
	}
	return f.findMethod(name), nil
}

func (f *fakePaymentSetup) PickPaymentMethod(_ context.Context, names []string) (*PaymentMethodSnapshot, error) {
	var best *PaymentMethodSnapshot
	for _, n := range names {
		row := f.findMethod(n)
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

func (f *fakePaymentSetup) BankAccountForAccount(_ context.Context, accountID int64) (*BankAccountSnapshot, error) {
	return f.banks[accountID], nil
}

func ptr(v int64) *int64 { return &v }

type fakeCommunicationGateway bool

func (f fakeCommunicationGateway) Enabled() bool { return bool(f) }

type fakeFiatControllerChecker struct{ err error }

func (f fakeFiatControllerChecker) Check(context.Context, string) error { return f.err }

// okPaymentSetup returns a catalog where Revolut and SEPA are linked+active
// and valid.
func okPaymentSetup() *fakePaymentSetup {
	return &fakePaymentSetup{
		methods: map[string]*PaymentMethodSnapshot{
			"Revolut": {Name: "Revolut", Category: "Fiat Transmitter", AccountID: ptr(1), IsActive: true, IsLinked: true, Priority: 5},
			"SEPA":    {Name: "SEPA", Category: "Bank", AccountID: ptr(2), IsActive: true, IsLinked: true, Priority: 1},
		},
		accounts: map[int64]*FiatAccountSnapshot{
			1: {Provider: "Revolut", Email: "user@example.com", PaymentTag: "@revtag"},
		},
		banks: map[int64]*BankAccountSnapshot{
			2: {Scheme: "iban_bic", Details: map[string]string{"iban": "DE89370400440532013000"}, IsActive: true},
		},
	}
}

func newTestChecker(
	balance func() (float64, error),
	whitelist func() ([]string, error),
	perms func() (map[string]string, error),
	walletAddrs func() ([]string, error),
	paymentSetup PaymentSetupFetcher,
) *BidChecker {
	return newTestCheckerWithPerms(
		balance,
		whitelist,
		func(_ string) (map[string]string, error) { return perms() },
		walletAddrs,
		paymentSetup,
	)
}

func newTestCheckerWithPerms(
	balance func() (float64, error),
	whitelist func() ([]string, error),
	permsForProvider func(provider string) (map[string]string, error),
	walletAddrs func() ([]string, error),
	paymentSetup PaymentSetupFetcher,
) *BidChecker {
	c := NewBidChecker(
		BalanceFetcherFunc(func(context.Context, string, string) (float64, error) { return balance() }),
		WhitelistFetcherFunc(func(context.Context, string) ([]string, error) { return whitelist() }),
		PermissionCheckerFunc(func(_ context.Context, provider string) (map[string]string, error) {
			return permsForProvider(provider)
		}),
		paymentSetup,
		nil,
	)
	c.SetCommunicationGateway(fakeCommunicationGateway(true))
	c.SetFiatControllerChecker(fakeFiatControllerChecker{})
	if walletAddrs != nil {
		c.SetWallet(WalletSourceFunc(func(context.Context) ([]string, error) { return walletAddrs() }))
		c.SetLNDOnline(LNDOnlineSourceFunc(func(context.Context) error { return nil }))
	}
	return c
}

func okPerms() (map[string]string, error) {
	return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermGranted}, nil
}

// paymentCheck extracts the payment_setup result from a report.
func paymentCheck(t *testing.T, report Report) CheckResult {
	t.Helper()
	return paymentCheckNamed(t, report, "payment_setup")
}

// paymentCheckNamed extracts the named check result from a report.
func paymentCheckNamed(t *testing.T, report Report, name string) CheckResult {
	t.Helper()
	for _, chk := range report.Checks {
		if chk.Name == name {
			return chk
		}
	}
	t.Fatalf("%s check missing from report", name)
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
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
	if !report.OK {
		t.Fatalf("expected OK, got %+v", report)
	}
	if len(report.Checks) != 9 {
		t.Fatalf("expected 9 checks, got %d", len(report.Checks))
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
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
	if report.OK {
		t.Fatal("expected report.OK=false")
	}
	if len(report.Checks) != 9 {
		t.Fatalf("expected all 9 checks reported, got %d", len(report.Checks))
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
	if chk := byName["communication_gateway"]; !chk.Passed {
		t.Errorf("communication_gateway should pass with configured channel, got %+v", chk)
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
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
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
	if !passed["communication_gateway"] {
		t.Errorf("expected communication_gateway to pass, got %+v", passed)
	}
	if passed["whitelist"] {
		t.Error("expected whitelist to fail")
	}
	if report.LNDWithdrawAddress != "" {
		t.Errorf("LNDWithdrawAddress should be empty when whitelist fails, got %q", report.LNDWithdrawAddress)
	}
}

func TestRunMakesBinanceWhitelistCredentialErrorActionable(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) {
			return nil, fmt.Errorf(`binance: GET /sapi/v1/capital/withdraw/address/list returned HTTP 400: {"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`)
		},
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)

	report := c.Run(context.Background(), PreTradeParams{
		Provider: "binance", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
	chk := paymentCheckNamed(t, report, "whitelist")
	if chk.Passed {
		t.Fatal("expected whitelist check to fail")
	}
	want := "Binance API access denied. Check the API key, allowed IP, and withdrawal permissions."
	if chk.Message != want {
		t.Fatalf("whitelist message = %q, want %q", chk.Message, want)
	}
	for _, rawDetail := range []string{"-2015", "/sapi/", "HTTP 400"} {
		if strings.Contains(chk.Message, rawDetail) {
			t.Fatalf("user-facing message contains raw API detail %q: %q", rawDetail, chk.Message)
		}
	}
}

type fakeChatTemplates map[string]string

func (f fakeChatTemplates) GetChatTemplate(_ context.Context, exchange, paymentMethodName string) (string, error) {
	return f[exchange+"|"+paymentMethodName], nil
}

func TestMessageTemplateNotApplicableForStructuredVenue(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	// No chat template store configured — a structured venue (Bisq v1) must still pass.
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "bisq",
	})
	chk := paymentCheckNamed(t, report, "message_template")
	if !chk.Passed {
		t.Errorf("expected message_template to pass for a structured venue, got %+v", chk)
	}
}

func TestMessageTemplateMissingFailsForChatVenue(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetChatTemplates(fakeChatTemplates{})
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "bisq2",
	})
	if report.OK {
		t.Fatal("expected OK=false when no message template is saved for a chat venue")
	}
	chk := paymentCheckNamed(t, report, "message_template")
	if chk.Passed || !strings.Contains(chk.Message, "no message template is saved") {
		t.Fatalf("message_template check = %+v", chk)
	}
}

func TestMessageTemplatePresentPassesForChatVenue(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0.01, nil }))
	withLightningSources(c, 0.05, 0.05, true)
	c.SetChatTemplates(fakeChatTemplates{"robosats|Revolut": "custom template"})
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "robosats",
		RequiredBTCLiquidity: 0.02,
	})
	if !report.OK {
		t.Fatalf("expected OK, got %+v", report)
	}
	chk := paymentCheckNamed(t, report, "message_template")
	if !chk.Passed {
		t.Errorf("expected message_template to pass, got %+v", chk)
	}
}

func TestCommunicationGatewayMissingFails(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetCommunicationGateway(nil)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
	if report.OK {
		t.Fatal("expected OK=false when communication gateway is missing")
	}
	for _, chk := range report.Checks {
		if chk.Name == "communication_gateway" {
			if chk.Passed || !strings.Contains(chk.Message, "no active Telegram communication channel") {
				t.Fatalf("communication_gateway check = %+v", chk)
			}
			return
		}
	}
	t.Fatal("communication_gateway check missing")
}

func TestCommunicationGatewayDisabledFailsRunAll(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetCommunicationGateway(fakeCommunicationGateway(false))
	err := c.RunAll(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
	if err == nil || !strings.Contains(err.Error(), "no active Telegram communication channel") {
		t.Fatalf("RunAll error = %v", err)
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
	err := c.RunAll(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
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
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", PaymentMethods: nil, Dex: "",
	})
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
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: method, PaymentMethods: methods, Dex: "",
	})
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
	setup.accounts[1].PaymentTag = ""
	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed || !strings.Contains(chk.Message, "missing payment tag") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupUnlinkedBankRail(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["SEPA"].AccountID = nil
	setup.methods["SEPA"].IsLinked = false
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not linked to a Bank account") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountInactive(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[2].IsActive = false
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "inactive") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountWrongScheme(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[2].Scheme = "sort_code"
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "expected iban_bic") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupBankAccountMissingIBAN(t *testing.T) {
	setup := okPaymentSetup()
	setup.banks[2].Details = map[string]string{}
	_, chk := runPaymentCheck(t, setup, "SEPA", nil)
	if chk.Passed || !strings.Contains(chk.Message, "no IBAN") {
		t.Fatalf("payment_setup check = %+v", chk)
	}
}

func TestPaymentSetupCryptoUnsupported(t *testing.T) {
	setup := okPaymentSetup()
	setup.methods["Lightning"] = &PaymentMethodSnapshot{
		Name: "Lightning", Category: "Crypto", IsActive: true,
	}
	_, chk := runPaymentCheck(t, setup, "Lightning", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not supported for automated trading") {
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

func TestPaymentSetupCatalogReadErrorIsOperatorFriendly(t *testing.T) {
	setup := okPaymentSetup()
	setup.methodErr = fmt.Errorf(`sql: Scan error on column index 13, name "alias_list": unsupported Scan`)

	_, chk := runPaymentCheck(t, setup, "Revolut", nil)
	if chk.Passed {
		t.Fatalf("payment_setup check unexpectedly passed: %+v", chk)
	}
	if !strings.Contains(chk.Message, "Revolut payment configuration could not be read") {
		t.Fatalf("payment_setup message is not actionable: %q", chk.Message)
	}
	if strings.Contains(chk.Message, "sql:") || strings.Contains(chk.Message, "alias_list") {
		t.Fatalf("payment_setup message leaks database internals: %q", chk.Message)
	}
}

func TestPaymentSetupResolvesExplicitAliasToParent(t *testing.T) {
	setup := okPaymentSetup()
	setup.aliases = map[string][]string{"SEPA": {"SEPA Instant"}}
	report, chk := runPaymentCheck(t, setup, "SEPA Instant", nil)
	if !chk.Passed {
		t.Fatalf("payment_setup check = %+v", chk)
	}
	if report.ResolvedPaymentMethod != "SEPA" {
		t.Errorf("ResolvedPaymentMethod: want SEPA, got %q", report.ResolvedPaymentMethod)
	}
}

func TestPaymentSetupPicksLinkedParentViaAlias(t *testing.T) {
	setup := okPaymentSetup()
	setup.aliases = map[string][]string{"SEPA": {"SEPA Instant"}}
	report, chk := runPaymentCheck(t, setup, "", []string{"SEPA Instant"})
	if !chk.Passed {
		t.Fatalf("payment_setup check = %+v", chk)
	}
	if report.ResolvedPaymentMethod != "SEPA" {
		t.Errorf("ResolvedPaymentMethod: want SEPA, got %q", report.ResolvedPaymentMethod)
	}
}

func TestPaymentSetupUnknownAliasStaysCatalogError(t *testing.T) {
	_, chk := runPaymentCheck(t, okPaymentSetup(), "Frobnicate Instant", nil)
	if chk.Passed || !strings.Contains(chk.Message, "not in the payment methods catalog") {
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

func TestPermissionsHedgelessAllowsMissingHedge(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func() (map[string]string, error) {
			return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", TradeMode: TradeModeHedgelessDoubleCollateral,
	})
	chk := paymentCheckNamed(t, report, "permissions")
	if !chk.Passed {
		t.Fatalf("hedgeless permissions should pass without hedge: %+v", chk)
	}
}

func TestPermissionsHedgefullRequiresHedgeOnSpotProvider(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func() (map[string]string, error) {
			return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", TradeMode: TradeModeHedgefullSingleCollateral,
		HedgeVenue: "kraken_futures",
	})
	chk := paymentCheckNamed(t, report, "permissions")
	if chk.Passed || !strings.Contains(chk.Message, "hedge position permission") {
		t.Fatalf("hedgefull permissions = %+v", chk)
	}
}

func TestPermissionsDecoupledChecksHedgeVenueSeparately(t *testing.T) {
	c := newTestCheckerWithPerms(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func(provider string) (map[string]string, error) {
			switch strings.ToLower(provider) {
			case "kraken":
				// Spot-only on the spot venue — no hedge permission.
				return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
			case "coinbase":
				return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermGranted}, nil
			default:
				return nil, fmt.Errorf("unexpected provider %q", provider)
			}
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", TradeMode: TradeModeHedgefullSingleCollateral,
		HedgeVenue: "coinbase_futures",
	})
	chk := paymentCheckNamed(t, report, "permissions")
	if !chk.Passed {
		t.Fatalf("decoupled hedgefull should pass with hedge perms on coinbase: %+v", chk)
	}
}

func TestPermissionsDecoupledFailsWhenHedgeVenueLacksPermission(t *testing.T) {
	c := newTestCheckerWithPerms(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func(provider string) (map[string]string, error) {
			switch strings.ToLower(provider) {
			case "kraken":
				return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermGranted}, nil
			case "coinbase":
				return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
			default:
				return nil, fmt.Errorf("unexpected provider %q", provider)
			}
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", HedgeVenue: "coinbase_futures",
	})
	chk := paymentCheckNamed(t, report, "permissions")
	if chk.Passed || !strings.Contains(chk.Message, "coinbase") {
		t.Fatalf("expected coinbase hedge failure, got %+v", chk)
	}
}

func TestProviderForHedgeVenue(t *testing.T) {
	got, err := ProviderForHedgeVenue("coinbase_futures")
	if err != nil || got != "coinbase" {
		t.Fatalf("ProviderForHedgeVenue(coinbase_futures) = %q, %v", got, err)
	}
	if _, err := ProviderForHedgeVenue("unknown_venue"); err == nil {
		t.Fatal("expected error for unknown venue")
	}
}

func TestParseTradeModeRejectsUnknown(t *testing.T) {
	if _, err := ParseTradeMode("future_mode"); err == nil {
		t.Fatal("expected unsupported trade mode error")
	}
	if got, err := ParseTradeMode(""); err != nil || got != TradeModeHedgefullSingleCollateral {
		t.Fatalf("empty mode = %q, %v", got, err)
	}
}

func TestLiquidityPoolCheckOnlyInHedgeless(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0.05, nil }))

	hedgefull := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", RequiredBTCLiquidity: 0.115,
	})
	for _, chk := range hedgefull.Checks {
		if chk.Name == "liquidity_pool" {
			t.Fatal("liquidity_pool check must not run in hedgefull mode")
		}
	}

	hedgeless := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", TradeMode: TradeModeHedgelessDoubleCollateral,
		RequiredBTCLiquidity: 0.115,
	})
	chk := paymentCheckNamed(t, hedgeless, "liquidity_pool")
	if chk.Passed || !strings.Contains(chk.Message, "insufficient LND on-chain BTC") {
		t.Fatalf("liquidity_pool check = %+v", chk)
	}
}

func TestLiquidityPoolCheckPassesWhenWalletSufficient(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func() (map[string]string, error) {
			return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0.2, nil }))

	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut", TradeMode: TradeModeHedgelessDoubleCollateral,
		RequiredBTCLiquidity: 0.115,
	})
	chk := paymentCheckNamed(t, report, "liquidity_pool")
	if !chk.Passed {
		t.Fatalf("liquidity_pool check = %+v", chk)
	}
	if len(report.Checks) != 10 {
		t.Fatalf("expected 10 checks in hedgeless mode, got %d", len(report.Checks))
	}
	// Capital checks must sit first and adjacent: exchange fiat, then BTC pool.
	if report.Checks[0].Name != "capital" || report.Checks[0].Label != "Exchange fiat" {
		t.Fatalf("checks[0] = %+v, want capital / Exchange fiat", report.Checks[0])
	}
	if report.Checks[1].Name != "liquidity_pool" || report.Checks[1].Label != "BTC liquidity pool" {
		t.Fatalf("checks[1] = %+v, want liquidity_pool / BTC liquidity pool", report.Checks[1])
	}
}

func withLightningSources(c *BidChecker, outbound, inbound float64, active bool) {
	c.SetLightningBalance(LightningBalanceSourceFuncs{
		OutboundFn: func(context.Context) (float64, error) { return outbound, nil },
		InboundFn:  func(context.Context) (float64, error) { return inbound, nil },
	})
	c.SetLightningChannels(LightningChannelSourceFunc(func(context.Context) (bool, error) {
		return active, nil
	}))
}

func TestLightningLiquidityBidOutboundInsufficient(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	// Pool total (on-chain 1 + local 0.01) covers 0.02; directional outbound does not.
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 1, nil }))
	withLightningSources(c, 0.01, 1, true)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		TradeMode: TradeModeHedgelessDoubleCollateral, Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	})
	pool := paymentCheckNamed(t, report, "liquidity_pool")
	if !pool.Passed {
		t.Fatalf("liquidity_pool should pass when on-chain+local covers required: %+v", pool)
	}
	chk := paymentCheckNamed(t, report, "lightning_liquidity")
	if chk.Passed || !strings.Contains(chk.Message, "outbound Lightning") {
		t.Fatalf("lightning_liquidity check = %+v", chk)
	}
	if chk.Label != "Self-funded Lightning outbound liquidity" {
		t.Fatalf("label = %q", chk.Label)
	}
}

func TestHedgelessSwapRunsPoolAndLightningComplementary(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	// Both fail independently: pool total 0.01 < 0.02, outbound 0.01 < 0.02.
	// Proves liquidity_pool still runs for Swap (not replaced by lightning_liquidity).
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0, nil }))
	withLightningSources(c, 0.01, 0, true)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		TradeMode: TradeModeHedgelessDoubleCollateral, Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	})
	if report.Checks[0].Name != "capital" || report.Checks[1].Name != "liquidity_pool" || report.Checks[2].Name != "lightning_liquidity" {
		t.Fatalf("capital order = %q, %q, %q", report.Checks[0].Name, report.Checks[1].Name, report.Checks[2].Name)
	}
	pool := paymentCheckNamed(t, report, "liquidity_pool")
	if pool.Passed || !strings.Contains(pool.Message, "on-chain + local channel") {
		t.Fatalf("liquidity_pool check = %+v", pool)
	}
	ln := paymentCheckNamed(t, report, "lightning_liquidity")
	if ln.Passed || !strings.Contains(ln.Message, "outbound Lightning") {
		t.Fatalf("lightning_liquidity check = %+v", ln)
	}
}

func TestHedgelessSwapPoolAndLightningBothPass(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		func() (map[string]string, error) {
			return map[string]string{PermSpotTrading: PermGranted, PermHedgePositions: PermDenied}, nil
		},
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0.01, nil }))
	withLightningSources(c, 0.05, 0, true)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		TradeMode: TradeModeHedgelessDoubleCollateral, Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	})
	if !paymentCheckNamed(t, report, "liquidity_pool").Passed {
		t.Fatalf("liquidity_pool = %+v", paymentCheckNamed(t, report, "liquidity_pool"))
	}
	if !paymentCheckNamed(t, report, "lightning_liquidity").Passed {
		t.Fatalf("lightning_liquidity = %+v", paymentCheckNamed(t, report, "lightning_liquidity"))
	}
	if len(report.Checks) != 11 {
		t.Fatalf("expected 11 checks in hedgeless Swap mode, got %d", len(report.Checks))
	}
}

func TestLightningLiquidityAskInboundInsufficient(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	withLightningSources(c, 1, 0.01, true)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		Dex: "robosats", Settlement: "onchain_swap", MarketSide: "ask", RequiredBTCLiquidity: 0.02,
	})
	chk := paymentCheckNamed(t, report, "lightning_liquidity")
	if chk.Passed || !strings.Contains(chk.Message, "inbound Lightning") {
		t.Fatalf("lightning_liquidity check = %+v", chk)
	}
	if chk.Label != "Self-funded Lightning inbound liquidity" {
		t.Fatalf("label = %q", chk.Label)
	}
}

func TestLightningLiquidityRequiresActiveChannel(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	withLightningSources(c, 1, 1, false)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	})
	chk := paymentCheckNamed(t, report, "lightning_liquidity")
	if chk.Passed || !strings.Contains(chk.Message, "no active self-funded Lightning channel") {
		t.Fatalf("lightning_liquidity check = %+v", chk)
	}
}

func TestLightningLiquidityPassesBidAndRunsInHedgefull(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	withLightningSources(c, 0.05, 0, true)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	})
	chk := paymentCheckNamed(t, report, "lightning_liquidity")
	if !chk.Passed {
		t.Fatalf("lightning_liquidity check = %+v", chk)
	}
	for _, cchk := range report.Checks {
		if cchk.Name == "liquidity_pool" {
			t.Fatal("liquidity_pool must not run for hedgefull Swap")
		}
	}
}

func TestLightningLiquiditySkippedForNonSwap(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	withLightningSources(c, 0, 0, false)
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		Dex: "bisq2",
	})
	if report.LightningLiquiditySource != "" {
		t.Fatalf("non-Swap route reported Lightning source %q", report.LightningLiquiditySource)
	}
	for _, chk := range report.Checks {
		if chk.Name == "lightning_liquidity" {
			t.Fatal("lightning_liquidity must not run for non-Swap routes")
		}
	}
}

func TestLightningLiquidityUsesSelectedSource(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	withLightningSources(c, 0.05, 0, true)
	c.SetLSPStatus(pretrade.LSPStatusSourceFunc(func(context.Context) (lsp.Status, error) {
		return lsp.Status{LSPHealthy: false, Reason: "LSP channel is disabled"}, nil
	}))
	params := PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500, PaymentMethod: "Revolut",
		Dex: "robosats", Settlement: "onchain_swap", RequiredBTCLiquidity: 0.02,
	}

	params.LightningLiquiditySource = pretrade.LightningLiquidityOwnChannels
	own := c.Run(context.Background(), params)
	if own.LightningLiquiditySource != pretrade.LightningLiquidityOwnChannels {
		t.Fatalf("reported source = %q, want own_channels", own.LightningLiquiditySource)
	}
	if !paymentCheckNamed(t, own, "lightning_liquidity").Passed {
		t.Fatalf("own-channel check = %+v", paymentCheckNamed(t, own, "lightning_liquidity"))
	}
	for _, check := range own.Checks {
		if check.Name == "lsp_channel" {
			t.Fatalf("own-channel route must not run LSP check: %+v", check)
		}
	}

	params.LightningLiquiditySource = pretrade.LightningLiquidityLSPChannel
	lspReport := c.Run(context.Background(), params)
	if lspReport.LightningLiquiditySource != pretrade.LightningLiquidityLSPChannel {
		t.Fatalf("reported source = %q, want lsp_channel", lspReport.LightningLiquiditySource)
	}
	check := paymentCheckNamed(t, lspReport, "lsp_channel")
	if check.Passed || !strings.Contains(check.Message, "LSP channel is disabled") {
		t.Fatalf("LSP check = %+v", check)
	}
	for _, check := range lspReport.Checks {
		if check.Name == "lightning_liquidity" {
			t.Fatalf("LSP route must not run own-channel check: %+v", check)
		}
	}
}

func TestLNDWalletOnlineCheckFailsWhenOffline(t *testing.T) {
	c := newTestChecker(
		func() (float64, error) { return 1000, nil },
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPerms,
		func() ([]string, error) { return []string{"bc1qwalletaddr"}, nil },
		okPaymentSetup(),
	)
	c.SetLNDOnline(LNDOnlineSourceFunc(func(context.Context) error {
		return fmt.Errorf("connection refused")
	}))
	report := c.Run(context.Background(), PreTradeParams{
		Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 500,
		PaymentMethod: "Revolut",
	})
	chk := paymentCheckNamed(t, report, "lnd_wallet")
	if chk.Passed || !strings.Contains(chk.Message, "connection refused") {
		t.Fatalf("lnd_wallet check = %+v", chk)
	}
	if report.OK {
		t.Fatal("expected report.OK=false when LND is offline")
	}
}
