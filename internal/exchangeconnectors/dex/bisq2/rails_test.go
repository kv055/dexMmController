package bisq2

import "testing"

func TestFiatRailName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"SEPA":          "SEPA",
		"SEPA Instant":  "SEPA_INSTANT",
		"sepa_instant":  "SEPA_INSTANT",
		"National Bank": "NATIONAL_BANK",
		"N26":           "MONEY_BEAM",
		"Revolut":       "REVOLUT",
		"Bizum":         "BIZUM",
	}
	for in, want := range cases {
		if got := FiatRailName(in); got != want {
			t.Errorf("FiatRailName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickBitcoinRail(t *testing.T) {
	t.Parallel()
	if got := PickBitcoinRail([]string{"LN", "MAIN_CHAIN"}); got != BitcoinRailMainChain {
		t.Fatalf("prefer MAIN_CHAIN, got %q", got)
	}
	if got := PickBitcoinRail([]string{"LN"}); got != BitcoinRailLN {
		t.Fatalf("LN-only = %q, want LN", got)
	}
	if got := PickBitcoinRail(nil); got != BitcoinRailMainChain {
		t.Fatalf("empty = %q, want MAIN_CHAIN", got)
	}
}

func TestMatchFiatRail(t *testing.T) {
	t.Parallel()
	if got := MatchFiatRail("SEPA Instant", []string{"REVOLUT", "SEPA_INSTANT"}); got != "SEPA_INSTANT" {
		t.Fatalf("got %q, want SEPA_INSTANT", got)
	}
	if got := MatchFiatRail("", []string{"SEPA"}); got != "SEPA" {
		t.Fatalf("sole rail = %q, want SEPA", got)
	}
}

func TestClassifyBitcoinPaymentData(t *testing.T) {
	t.Parallel()
	addr, inv := ClassifyBitcoinPaymentData("bc1qexample")
	if addr != "bc1qexample" || inv != "" {
		t.Fatalf("address classify = (%q, %q)", addr, inv)
	}
	addr, inv = ClassifyBitcoinPaymentData("lnbc1invoice")
	if addr != "" || inv != "lnbc1invoice" {
		t.Fatalf("invoice classify = (%q, %q)", addr, inv)
	}
}
