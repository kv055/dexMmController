package api

import (
	"dexTakerController/internal/db"
	"encoding/json"
	"testing"
	"time"
)

func TestStoredQuoteRejectsChangedExecutionIdentityAndCaps(t *testing.T) {
	q := &db.ExecutionQuote{ID: "quote", Exchange: "robosats", CoordinatorID: "coordinator", OfferID: "42", PaymentMethod: "SEPA", MaxBondSat: 30000, MaxEscrowSat: 1000000, BondFeeSat: 5, EscrowFeeSat: 5, ExpiresAt: time.Now().Add(time.Minute), Route: &db.ResolvedRoute{}}
	cases := []struct {
		name string
		req  tradeflowRequest
	}{
		{"exchange", tradeflowRequest{Dex: "bisq"}},
		{"offer", tradeflowRequest{OfferID: "99"}},
		{"transaction", tradeflowRequest{TransactionID: 123}},
		{"method", tradeflowRequest{PaymentMethod: "cash"}},
		{"coordinator", tradeflowRequest{SelectedOffer: json.RawMessage(`{"venue_coordinator_id":"other"}`)}},
		{"escrow", tradeflowRequest{SelectedOffer: json.RawMessage(`{"max_escrow_sat":2000000}`)}},
		{"bond", tradeflowRequest{SelectedOffer: json.RawMessage(`{"max_bond_sat":50000}`)}},
		{"quote", tradeflowRequest{SelectedOffer: json.RawMessage(`{"quote_id":"other"}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := applyStoredQuote(&tc.req, q, time.Now()); err == nil {
				t.Fatal("changed terms accepted")
			}
		})
	}
	req := tradeflowRequest{SelectedOffer: json.RawMessage(`{"max_escrow_sat":1000000}`)}
	if err := applyStoredQuote(&req, q, time.Now()); err != nil {
		t.Fatalf("matching numeric JSON rejected: %v", err)
	}
	if req.Dex != "robosats" || req.OfferID != "42" || req.QuoteID != "quote" {
		t.Fatalf("not bound to stored identity: %+v", req)
	}
}

func TestStoredQuoteExpiryOnlyAppliesToAdmission(t *testing.T) {
	q := &db.ExecutionQuote{ID: "q", Exchange: "robosats", OfferID: "42", Route: &db.ResolvedRoute{}, ExpiresAt: time.Now().Add(-time.Minute)}
	if err := applyStoredQuote(&tradeflowRequest{}, q, time.Now()); err == nil {
		t.Fatal("expired unbound quote accepted")
	}
	q.TransactionID = 7
	req := tradeflowRequest{}
	if err := applyStoredQuote(&req, q, time.Now()); err != nil || req.TransactionID != 7 {
		t.Fatalf("bound retry not recovered: %v", err)
	}
}
