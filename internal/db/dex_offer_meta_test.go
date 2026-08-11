package db

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDexPlatformFromSlug(t *testing.T) {
	// Covers all five supported DEX venues and casing/whitespace normalization.
	// normalization; bisq2 and peachbitcoin are the ones the old 3-venue mapper missed.
	cases := map[string]DexPlatform{
		"bisq":          DexBisq,
		"bisq2":         DexBisq2,
		"hodlhodl":      DexHodlHodl,
		"robosats":      DexRoboSats,
		"peachbitcoin":  DexPeachBitcoin,
		" PeachBitcoin": DexPeachBitcoin,
		"BISQ2":         DexBisq2,
	}
	for slug, want := range cases {
		if got := DexPlatformFromSlug(slug); got != want {
			t.Errorf("DexPlatformFromSlug(%q) = %q, want %q", slug, got, want)
		}
	}
	// Unknown slugs pass through unchanged (raw value).
	if got := DexPlatformFromSlug("mystery"); got != DexPlatform("mystery") {
		t.Errorf("unknown slug should pass through, got %q", got)
	}
}

func TestParseDexOfferMetaOfferAndContract(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"selected_offer": map[string]any{
			"offer_id": "offer-1",
			"exchange": "hodlhodl",
		},
		"lnd_receive_address": "bc1qtest",
		"cex_withdrawal": map[string]any{
			"withdrawal_id": "w1",
			"provider":      "binance",
		},
		"execution": map[string]any{
			"exchange":       "hodlhodl",
			"offer_id":       "offer-1",
			"contract_id":    "c99",
			"escrow_address": "2Nabc",
		},
	})

	meta, err := ParseDexOfferCreation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OfferID() != "offer-1" {
		t.Fatalf("OfferID: got %q", meta.OfferID())
	}
	if meta.ContractID() != "c99" {
		t.Fatalf("ContractID: got %q", meta.ContractID())
	}
	if meta.EscrowOrDepositAddress() != "2Nabc" {
		t.Fatalf("escrow: got %q", meta.EscrowOrDepositAddress())
	}
	if meta.LNDReceiveAddress != "bc1qtest" {
		t.Fatalf("lnd address: got %q", meta.LNDReceiveAddress)
	}
}

func TestHasRelayCompleted(t *testing.T) {
	if HasRelayCompleted(nil) {
		t.Fatal("empty should not be complete")
	}
	raw, _ := json.Marshal(CexToDexRelayDetails{
		TxID:    "abc123",
		SentAt:  time.Now().UTC(),
	})
	if !HasRelayCompleted(raw) {
		t.Fatal("txid should count as relay complete")
	}
}

func TestConfirmationsGate(t *testing.T) {
	if !WithdrawalConfirmationsMet(1) {
		t.Fatal("1 conf should pass")
	}
	if WithdrawalConfirmationsMet(0) {
		t.Fatal("0 conf should not pass")
	}
}
