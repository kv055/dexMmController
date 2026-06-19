package cexaccount

import (
	"testing"

	"dexMmController/internal/exchanges/cex/kraken"
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
