package robosats

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNativeQuoteUsesCoordinatorPriceAndIncludesSellerFee(t *testing.T) {
	var p Position
	raw := `{"id":42,"status":1,"type":0,"currency":2,"has_range":true,"min_amount":"50","max_amount":"200","payment_method":"SEPA","price_now":"100000","bond_size":"3","expires_at":"2030-01-01T00:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	q, err := quoteFromPosition(&p, QuoteInput{OrderID: "42", CoordinatorID: "coord", Amount: "100", Unit: "fiat", Currency: "EUR", PaymentMethod: "SEPA"}, "0.001", "node", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.PrincipalSat != 100000 || q.MaxBondSat != 3000 || q.MaxEscrowSat != 100100 {
		t.Fatalf("unexpected caps: %+v", q)
	}
	p.Status = StatusPaused
	if _, err = quoteFromPosition(&p, QuoteInput{OrderID: "42"}, "0.001", "node", time.Now()); err == nil {
		t.Fatal("paused order quoted")
	}
}

func TestDecimalRejectsNonDecimalInput(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "1/2", "1e8", "-1", ""} {
		if _, err := decimal(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
