package cexaccount

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/coinbase"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
)

type credentialRepo struct {
	keysByProvider map[string][]db.APIKey
}

func (r credentialRepo) GetActiveAPIKeysForProvider(_ context.Context, provider string) ([]db.APIKey, error) {
	return r.keysByProvider[provider], nil
}

func (credentialRepo) GetTransactionsByCEX(string) ([]db.Transaction, error) {
	return nil, nil
}

func TestFuturesKeyUsesActiveDatabaseCredentials(t *testing.T) {
	service := NewService(credentialRepo{keysByProvider: map[string][]db.APIKey{
		"Kraken":   {{APIKey: "kraken-key", APISecret: "kraken-secret"}},
		"Binance":  {{APIKey: "binance-key", APISecret: "binance-secret"}},
		"Coinbase": {{APIKey: "spot-key", APISecret: "spot-secret"}, {APIKey: "intx-key", APISecret: "intx-secret", Passphrase: "intx-pass"}},
	}}, Config{})

	tests := map[string]db.APIKey{
		"kraken_futures":   {APIKey: "kraken-key", APISecret: "kraken-secret"},
		"binance_futures":  {APIKey: "binance-key", APISecret: "binance-secret"},
		"coinbase_futures": {APIKey: "intx-key", APISecret: "intx-secret", Passphrase: "intx-pass"},
	}
	for venue, want := range tests {
		got, err := service.futuresKey(context.Background(), venue)
		if err != nil {
			t.Fatalf("futuresKey(%q): %v", venue, err)
		}
		if got.APIKey != want.APIKey || got.APISecret != want.APISecret || got.Passphrase != want.Passphrase {
			t.Fatalf("futuresKey(%q) = %#v, want %#v", venue, got, want)
		}
	}
}

func TestCoinbaseFuturesKeyPrefersHedgeGrantedPassphraseKey(t *testing.T) {
	denied, _ := json.Marshal(map[string]string{PermHedgePositions: PermDenied})
	granted, _ := json.Marshal(map[string]string{PermHedgePositions: PermGranted})
	service := NewService(credentialRepo{keysByProvider: map[string][]db.APIKey{
		"Coinbase": {
			{APIKey: "spot-key", APISecret: "spot-secret"},
			{APIKey: "exchange-key", APISecret: "exchange-secret", Passphrase: "ex-pass", Permissions: denied},
			{APIKey: "intx-key", APISecret: "intx-secret", Passphrase: "intx-pass", Permissions: granted},
		},
	}}, Config{})

	got, err := service.futuresKey(context.Background(), "coinbase_futures")
	if err != nil {
		t.Fatalf("futuresKey: %v", err)
	}
	if got.APIKey != "intx-key" {
		t.Fatalf("futuresKey = %q, want intx-key", got.APIKey)
	}
}

func TestKrakenSpotAndFuturesKeySelection(t *testing.T) {
	spotPerms, _ := json.Marshal(map[string]string{
		PermFetchDepositAddress: PermGranted,
		PermSpotTrading:         PermGranted,
		PermReadBalances:        PermGranted,
		PermWithdraw:            PermGranted,
		PermHedgePositions:      PermDenied,
	})
	futuresPerms, _ := json.Marshal(map[string]string{
		PermFetchDepositAddress: PermDenied,
		PermSpotTrading:         PermDenied,
		PermReadBalances:        PermDenied,
		PermWithdraw:            PermDenied,
		PermHedgePositions:      PermGranted,
	})
	service := NewService(credentialRepo{keysByProvider: map[string][]db.APIKey{
		"Kraken": {
			{ID: 7, Name: "futures", APIKey: "futures-key", APISecret: "futures-secret", Permissions: futuresPerms},
			{ID: 2, Name: "spot", APIKey: "spot-key", APISecret: "spot-secret", Permissions: spotPerms},
		},
	}}, Config{})

	_, spot, err := service.spotAPIKey(context.Background(), "Kraken")
	if err != nil {
		t.Fatalf("spotAPIKey: %v", err)
	}
	if spot.APIKey != "spot-key" {
		t.Fatalf("spotAPIKey = %q, want spot-key", spot.APIKey)
	}
	futures, err := service.futuresKey(context.Background(), "kraken_futures")
	if err != nil {
		t.Fatalf("futuresKey: %v", err)
	}
	if futures.APIKey != "futures-key" {
		t.Fatalf("futuresKey = %q, want futures-key", futures.APIKey)
	}
}

func TestSpotOperationSelectsComplementaryPermissionKey(t *testing.T) {
	tradePerms, _ := json.Marshal(map[string]string{PermSpotTrading: PermGranted, PermWithdraw: PermDenied})
	withdrawPerms, _ := json.Marshal(map[string]string{PermSpotTrading: PermDenied, PermWithdraw: PermGranted})
	service := NewService(credentialRepo{keysByProvider: map[string][]db.APIKey{
		"Binance": {
			{APIKey: "trade-key", APISecret: "trade-secret", Permissions: tradePerms},
			{APIKey: "withdraw-key", APISecret: "withdraw-secret", Permissions: withdrawPerms},
		},
	}}, Config{})

	_, tradeKey, err := service.spotAPIKeyFor(context.Background(), "Binance", PermSpotTrading)
	if err != nil || tradeKey.APIKey != "trade-key" {
		t.Fatalf("trade key = %q, err=%v", tradeKey.APIKey, err)
	}
	_, withdrawKey, err := service.spotAPIKeyFor(context.Background(), "Binance", PermWithdraw)
	if err != nil || withdrawKey.APIKey != "withdraw-key" {
		t.Fatalf("withdraw key = %q, err=%v", withdrawKey.APIKey, err)
	}
}

func TestCanonicalProvider(t *testing.T) {
	tests := map[string]string{
		"kraken":    "Kraken",
		"BINANCE":   "Binance",
		"coinbase":  "Coinbase",
		"hodl hodl": "HodlHodl",
		"HodlHodl":  "HodlHodl",
		"robosats":  "Robosats",
		"RoboSats":  "Robosats",
	}
	for input, want := range tests {
		got, err := CanonicalProvider(input)
		if err != nil {
			t.Fatalf("CanonicalProvider(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("CanonicalProvider(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := CanonicalProvider("Bisq"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("CanonicalProvider(Bisq) error = %v, want ErrUnsupportedProvider", err)
	}
}

func TestParseIntervalDefaultIsTenMinutes(t *testing.T) {
	for _, raw := range []string{"", "nope", "-1s", "0s"} {
		if got := ParseInterval(raw); got != 10*time.Minute {
			t.Fatalf("ParseInterval(%q) = %s, want 10m", raw, got)
		}
	}
	if got := ParseInterval("30s"); got != 30*time.Second {
		t.Fatalf("ParseInterval(30s) = %s, want 30s", got)
	}
}

func TestHedgePositionFromTransactionActiveVsPast(t *testing.T) {
	openDetails, _ := json.Marshal(map[string]string{
		"venue":      "kraken_futures",
		"instrument": "PI_XBTUSD",
	})
	closeDetails, _ := json.Marshal(map[string]string{"status": "closed"})

	active := hedgePositionFromTransaction(db.Transaction{
		ID:                            1,
		Direction:                     "bid",
		Status:                        db.StatusCexTradeSuccessful,
		CexBitcoinAmount:              0.01,
		CexCounterPositionOpenDetails: openDetails,
		CexCounterPositionFee:         0.0001,
		CreatedAt:                     time.Now().UTC(),
	})
	if active.Venue != "" && active.Instrument != "PI_XBTUSD" {
		t.Fatalf("instrument = %q, want PI_XBTUSD", active.Instrument)
	}

	past := db.Transaction{
		ID:                             2,
		Direction:                      "ask",
		Status:                         db.StatusDexPositionReleased,
		CexCounterPositionOpenDetails:  openDetails,
		CexCounterPositionCloseDetails: closeDetails,
		TransactionComplete:            true,
		UpdatedAt:                      time.Now().UTC(),
	}
	if !hasJSONData(past.CexCounterPositionCloseDetails) {
		t.Fatal("expected close details to be present")
	}
}

func TestMarketBuyPair(t *testing.T) {
	cases := []struct {
		provider, fiat, want string
	}{
		{"kraken", "EUR", "XBTEUR"},
		{"Kraken", "usd", "XBTUSD"},
		{"coinbase", "EUR", "BTC-EUR"},
		{"binance", "EUR", "BTCEUR"},
		{"binance", "", "BTCEUR"},
	}
	for _, c := range cases {
		if got := marketBuyPair(c.provider, c.fiat); got != c.want {
			t.Fatalf("marketBuyPair(%q,%q) = %q, want %q", c.provider, c.fiat, got, c.want)
		}
	}
}

func TestMaxMarketBuyBTCDefault(t *testing.T) {
	t.Setenv("MAX_MARKET_BUY_BTC", "")
	if got := maxMarketBuyBTC(); got != defaultMaxMarketBuyBTC {
		t.Fatalf("maxMarketBuyBTC() = %v, want default %v", got, defaultMaxMarketBuyBTC)
	}
	t.Setenv("MAX_MARKET_BUY_BTC", "0.25")
	if got := maxMarketBuyBTC(); got != 0.25 {
		t.Fatalf("maxMarketBuyBTC() = %v, want 0.25", got)
	}
}

func TestMarketBuyBTCRejectsInvalidAmounts(t *testing.T) {
	t.Setenv("MAX_MARKET_BUY_BTC", "1.0")
	svc := &Service{}
	base := PreTradeParams{Provider: "kraken", FiatCurrency: "EUR", RequiredCapital: 100, PaymentMethod: "Revolut"}
	if _, err := svc.MarketBuyBTC(context.Background(), 0, base); err == nil {
		t.Fatal("expected error for zero amount")
	}
	if _, err := svc.MarketBuyBTC(context.Background(), 5, base); err == nil {
		t.Fatal("expected error for amount over cap")
	}
}

func TestMarketBuyBTCRequiresCapitalRequirement(t *testing.T) {
	t.Setenv("MAX_MARKET_BUY_BTC", "1.0")
	svc := &Service{}
	if _, err := svc.MarketBuyBTC(context.Background(), 0.01, PreTradeParams{Provider: "kraken", FiatCurrency: "EUR", PaymentMethod: "Revolut"}); err == nil {
		t.Fatal("expected error when required_capital is not provided")
	}
}

func TestBalancesFromCoinbase(t *testing.T) {
	resp := balancesFromCoinbase([]coinbase.Account{
		{
			Currency:         "BTC",
			Active:           true,
			AvailableBalance: coinbase.AccountAmount{Value: "0.25", Currency: "BTC"},
		},
		{
			Currency:         "EUR",
			Active:           true,
			AvailableBalance: coinbase.AccountAmount{Value: "42.10", Currency: "EUR"},
		},
		{
			Currency:         "USD",
			Active:           true,
			AvailableBalance: coinbase.AccountAmount{Value: "0", Currency: "USD"},
		},
	})
	if resp.Bitcoin == nil || resp.Bitcoin.Total != 0.25 {
		t.Fatalf("bitcoin balance = %#v, want 0.25 BTC", resp.Bitcoin)
	}
	if len(resp.Fiat) != 2 {
		t.Fatalf("fiat balances = %#v, want EUR and zero USD", resp.Fiat)
	}
}

func TestNormalizeOrderStatus(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"closed", "filled"},
		{"FILLED", "filled"},
		{"PARTIALLY_FILLED", "partially_filled"},
		{"open", "open"},
		{"pending", "open"},
		{"NEW", "open"},
		{"canceled", "canceled"},
		{"CANCELLED", "canceled"},
		{"expired", "expired"},
		{"something-else", "unknown"},
		{"", "unknown"},
	}
	for _, c := range cases {
		if got := normalizeOrderStatus(c.raw); got != c.want {
			t.Fatalf("normalizeOrderStatus(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestExecutionFromKraken(t *testing.T) {
	exec, err := executionFromKraken(kraken.OrderInfo{
		Status:  "closed",
		VolExec: "0.01000000",
		Cost:    "650.00",
		Fee:     "1.04",
		Price:   "65000.00",
	}, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.FilledBTC != 0.01 || exec.AverageRate != 65000.00 || exec.FilledFiat != 650.00 {
		t.Fatalf("unexpected execution: %#v", exec)
	}
	if len(exec.Fees) != 1 || exec.Fees[0] != (FeeLine{Asset: "EUR", Amount: 1.04}) {
		t.Fatalf("unexpected fees: %#v", exec.Fees)
	}
	if exec.OrderStatus != "filled" {
		t.Fatalf("order status = %q, want filled", exec.OrderStatus)
	}

	// Zero fee should produce no fee lines at all.
	zeroFee, err := executionFromKraken(kraken.OrderInfo{
		Status: "closed", VolExec: "0.01", Cost: "650.00", Fee: "0", Price: "65000.00",
	}, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(zeroFee.Fees) != 0 {
		t.Fatalf("expected no fee lines, got %#v", zeroFee.Fees)
	}

	// "closed" with nothing executed is not a real fill.
	rejected, err := executionFromKraken(kraken.OrderInfo{
		Status: "closed", VolExec: "0", Cost: "0", Fee: "0", Price: "0",
	}, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rejected.OrderStatus != "unknown" {
		t.Fatalf("order status = %q, want unknown for closed-but-unfilled order", rejected.OrderStatus)
	}

	if _, err := executionFromKraken(kraken.OrderInfo{Price: "not-a-number"}, "EUR"); err == nil {
		t.Fatal("expected error for malformed price")
	}
}

func TestExecutionFromBinance(t *testing.T) {
	order := binance.OrderResponse{
		Status: "FILLED",
		Fills: []binance.OrderFill{
			{Price: "65000.00", Qty: "0.005", Commission: "0.0001", CommissionAsset: "BNB"},
			{Price: "65010.00", Qty: "0.005", Commission: "0.0001", CommissionAsset: "BNB"},
			{Price: "65000.00", Qty: "0.002", Commission: "0.10", CommissionAsset: "EUR"},
		},
	}
	exec, err := executionFromBinance(order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantBTC := 0.005 + 0.005 + 0.002
	if abs(exec.FilledBTC-wantBTC) > 1e-9 {
		t.Fatalf("filled btc = %v, want %v", exec.FilledBTC, wantBTC)
	}
	wantFiat := 65000.00*0.005 + 65010.00*0.005 + 65000.00*0.002
	if abs(exec.FilledFiat-wantFiat) > 1e-6 {
		t.Fatalf("filled fiat = %v, want %v", exec.FilledFiat, wantFiat)
	}
	if len(exec.Fees) != 2 {
		t.Fatalf("expected fees aggregated by asset, got %#v", exec.Fees)
	}
	if exec.Fees[0].Asset != "BNB" || abs(exec.Fees[0].Amount-0.0002) > 1e-9 {
		t.Fatalf("unexpected BNB fee line: %#v", exec.Fees[0])
	}
	if exec.Fees[1].Asset != "EUR" || abs(exec.Fees[1].Amount-0.10) > 1e-9 {
		t.Fatalf("unexpected EUR fee line: %#v", exec.Fees[1])
	}
	if exec.OrderStatus != "filled" {
		t.Fatalf("order status = %q, want filled", exec.OrderStatus)
	}

	// Fallback path when Fills is empty.
	fallback, err := executionFromBinance(binance.OrderResponse{
		Status: "FILLED", ExecutedQty: "0.01", CummulativeQuoteQty: "650.00",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fallback.FilledBTC != 0.01 || fallback.FilledFiat != 650.00 || fallback.AverageRate != 65000.00 {
		t.Fatalf("unexpected fallback execution: %#v", fallback)
	}
	if len(fallback.Fees) != 0 {
		t.Fatalf("expected no fees in fallback path, got %#v", fallback.Fees)
	}
}

func TestExecutionFromCoinbase(t *testing.T) {
	exec, err := executionFromCoinbase(coinbase.Order{
		Status:             "FILLED",
		AverageFilledPrice: "65000.00",
		FilledSize:         "0.01",
		TotalFees:          "3.25",
	}, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.FilledBTC != 0.01 || exec.AverageRate != 65000.00 {
		t.Fatalf("unexpected execution: %#v", exec)
	}
	if abs(exec.FilledFiat-650.00) > 1e-9 {
		t.Fatalf("filled fiat = %v, want 650.00 (recomputed from rate*size)", exec.FilledFiat)
	}
	if len(exec.Fees) != 1 || exec.Fees[0] != (FeeLine{Asset: "EUR", Amount: 3.25}) {
		t.Fatalf("unexpected fees: %#v", exec.Fees)
	}
	if exec.OrderStatus != "filled" {
		t.Fatalf("order status = %q, want filled", exec.OrderStatus)
	}

	if _, err := executionFromCoinbase(coinbase.Order{AverageFilledPrice: "nope"}, "EUR"); err == nil {
		t.Fatal("expected error for malformed average_filled_price")
	}
}

func TestPollUntilTerminalImmediateTerminal(t *testing.T) {
	calls := 0
	exhausted, err := pollUntilTerminal(context.Background(), 5, time.Millisecond, func(ctx context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if err != nil || exhausted {
		t.Fatalf("exhausted=%v err=%v, want exhausted=false err=nil", exhausted, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (should stop at first terminal result)", calls)
	}
}

func TestPollUntilTerminalExhausts(t *testing.T) {
	calls := 0
	exhausted, err := pollUntilTerminal(context.Background(), 3, time.Millisecond, func(ctx context.Context) (bool, error) {
		calls++
		return false, nil
	})
	if err != nil || !exhausted {
		t.Fatalf("exhausted=%v err=%v, want exhausted=true err=nil", exhausted, err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (maxAttempts)", calls)
	}
}

func TestPollUntilTerminalErrorOnFinalAttempt(t *testing.T) {
	calls := 0
	wantErr := errors.New("boom")
	exhausted, err := pollUntilTerminal(context.Background(), 2, time.Millisecond, func(ctx context.Context) (bool, error) {
		calls++
		if calls == 2 {
			return false, wantErr
		}
		return false, nil
	})
	if !exhausted || !errors.Is(err, wantErr) {
		t.Fatalf("exhausted=%v err=%v, want exhausted=true err=%v", exhausted, err, wantErr)
	}
}

func TestPollUntilTerminalRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	exhausted, err := pollUntilTerminal(ctx, 5, 50*time.Millisecond, func(ctx context.Context) (bool, error) {
		calls++
		return false, nil
	})
	if !exhausted || !errors.Is(err, context.Canceled) {
		t.Fatalf("exhausted=%v err=%v, want exhausted=true err=context.Canceled", exhausted, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (should stop after first attempt once ctx is canceled)", calls)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestBalancesFromKraken(t *testing.T) {
	resp := balancesFromKraken(map[string]string{
		"XXBT": "0.25",
		"ZEUR": "1500.50",
		"ZUSD": "0",
	})
	if resp.Bitcoin == nil || resp.Bitcoin.Total != 0.25 {
		t.Fatalf("bitcoin balance = %#v, want 0.25 BTC", resp.Bitcoin)
	}
	if len(resp.Fiat) != 1 || resp.Fiat[0].Asset != "EUR" {
		t.Fatalf("fiat balances = %#v, want EUR only", resp.Fiat)
	}
}
