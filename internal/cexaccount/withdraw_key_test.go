package cexaccount

import (
	"testing"

	"dexMmController/internal/exchangeconnectors/cex/kraken"
	lnd "dexMmController/internal/wallethub"
)

func TestKrakenWithdrawKeyForAddress(t *testing.T) {
	rows := []kraken.WithdrawAddress{
		{Address: "bc1qother", Key: "other", Verified: true},
		{Address: "bc1qtest", Key: "LND_wallet", Verified: true},
	}
	key, err := krakenWithdrawKeyForAddress(rows, "bc1qtest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "LND_wallet" {
		t.Fatalf("key = %q, want LND_wallet", key)
	}
}

func TestKrakenWithdrawKeyForAddressUnverifiedSkipped(t *testing.T) {
	rows := []kraken.WithdrawAddress{
		{Address: "bc1qtest", Key: "pending", Verified: false},
	}
	_, err := krakenWithdrawKeyForAddress(rows, "bc1qtest")
	if err == nil {
		t.Fatal("expected error for unverified address")
	}
}

func TestKrakenWithdrawKeyForAddressAmbiguous(t *testing.T) {
	rows := []kraken.WithdrawAddress{
		{Address: "bc1qtest", Key: "a", Verified: true},
		{Address: "bc1qtest", Key: "b", Verified: true},
	}
	_, err := krakenWithdrawKeyForAddress(rows, "bc1qtest")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
}

func TestBinanceWithdrawStatusLabel(t *testing.T) {
	if binanceWithdrawStatusLabel(6) != "completed" {
		t.Fatal("want completed for status 6")
	}
	if krakenWithdrawStatusLabel("Success") != "completed" {
		t.Fatal("want completed for Kraken Success")
	}
}

func TestBinanceWithdrawalStatusFromRecord_ParsesFeeBTC(t *testing.T) {
	status := binanceWithdrawalStatusFromRecord(lnd.ExchangeWithdrawRecord{
		ID:     "wd-1",
		Status: 6,
		TxID:   "abc",
		Amount: "0.01",
		Fee:    "0.0002",
	})
	if status.FeeBTC != 0.0002 {
		t.Errorf("fee_btc: want 0.0002, got %v", status.FeeBTC)
	}
}

func TestParseDecimal(t *testing.T) {
	if got := parseDecimal("0.0002"); got != 0.0002 {
		t.Errorf("parseDecimal(0.0002): got %v", got)
	}
	if got := parseDecimal(""); got != 0 {
		t.Errorf("parseDecimal(empty): want 0, got %v", got)
	}
	if got := parseDecimal("not-a-number"); got != 0 {
		t.Errorf("parseDecimal(invalid): want 0, got %v", got)
	}
}
