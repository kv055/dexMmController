package dexrelay

import (
	"encoding/json"
	"testing"

	"dexTakerController/internal/db"
)

func TestRelayAlreadyCompleted(t *testing.T) {
	if shouldSkipRelay(nil) {
		t.Fatal("nil details should not skip")
	}
	raw, _ := json.Marshal(db.CexToDexRelayDetails{TxID: "deadbeef"})
	if !shouldSkipRelay(raw) {
		t.Fatal("txid should skip relay")
	}
}

func TestTakeAmountsUseConfirmedBisqPlan(t *testing.T) {
	tx := &db.Transaction{CexBitcoinAmount: 0.0095, FiatAmount: 500}
	meta := db.DexOfferMeta{BisqExecution: &db.BisqExecutionPlan{
		TradeAmountSats:    1_000_000,
		FiatAmount:         550,
		PaymentAccountID:   "acct-9",
		MaxWalletDebitSats: 1_160_000,
		MinVolumeAsLong:    5_500_000,
	}}
	if got := takeBTCAmount(tx, meta, 950_000); got != 0.01 {
		t.Fatalf("btc = %v, want confirmed 0.01 not CEX fill", got)
	}
	if got := takeFiatAmount(tx, meta); got != 550 {
		t.Fatalf("fiat = %v, want Bisq volume", got)
	}
	if got := takePaymentAccountID(meta); got != "acct-9" {
		t.Fatalf("account = %q", got)
	}
	if takePaymentAccountID(db.DexOfferMeta{}) != "" {
		t.Fatal("legacy rows without a plan must not invent an account")
	}
}

func shouldSkipRelay(raw json.RawMessage) bool {
	return db.HasRelayCompleted(raw)
}
