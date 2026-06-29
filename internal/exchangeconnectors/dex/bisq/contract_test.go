package bisq

import (
	"encoding/json"
	"testing"
)

func TestMakerPaymentFromContract(t *testing.T) {
	t.Run("extracts maker payment account payload", func(t *testing.T) {
		contract := `{
			"isBuyerMakerAndSellerTaker": false,
			"makerPaymentAccountPayload": {"paymentMethodId": "SEPA", "holderName": "Alice", "iban": "DE89370400440532013000"},
			"takerPaymentAccountPayload": {"paymentMethodId": "SEPA", "holderName": "Bob", "iban": "DE00000000000000000000"}
		}`
		got, err := makerPaymentFromContract(contract)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(got, &payload); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if payload["iban"] != "DE89370400440532013000" {
			t.Fatalf("expected maker iban, got %v", payload["iban"])
		}
		if payload["holderName"] != "Alice" {
			t.Fatalf("expected maker holderName Alice, got %v", payload["holderName"])
		}
	})

	t.Run("falls back to whole contract when field absent", func(t *testing.T) {
		contract := `{"tradeAmount": 100000, "tradePrice": "50000"}`
		got, err := makerPaymentFromContract(contract)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(got, &payload); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if payload["tradeAmount"] == nil {
			t.Fatalf("expected the whole contract back, got %s", got)
		}
	})

	t.Run("errors on empty / invalid", func(t *testing.T) {
		if _, err := makerPaymentFromContract("  "); err == nil {
			t.Fatal("expected error on empty contract")
		}
		if _, err := makerPaymentFromContract("not json"); err == nil {
			t.Fatal("expected error on invalid contract json")
		}
	})
}
