package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalapi "dexTakerController/internal/api/gen/internalapi"
	"dexTakerController/internal/exchangeconnectors/dex/robosats"
	"dexTakerController/internal/tradeflow"
)

type quoteOnlyRunner struct{}

func (quoteOnlyRunner) Run(context.Context, tradeflow.Order) (*tradeflow.Result, error) {
	panic("quote must not execute a trade")
}
func (quoteOnlyRunner) QuoteRoboSats(context.Context, robosats.QuoteInput) (*robosats.NativeTerms, error) {
	return &robosats.NativeTerms{OrderID: "42", MaxEscrowSat: 100100}, nil
}
func TestNativeQuoteIsRegisteredAndReadOnly(t *testing.T) {
	s := &Server{tradeFlow: quoteOnlyRunner{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/robosats/quote", strings.NewReader(`{"offer_id":"42"}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"max_escrow_sat":100100`) {
		t.Fatalf("quote endpoint: %d %s", w.Code, w.Body.String())
	}
}
func TestGeneratedTradeflowContractAcceptsAdmissionWithoutFill(t *testing.T) {
	legacy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]any{"transaction_id": 42, "status_url": "/api/transactions/42"})
	})
	mux := http.NewServeMux()
	internalapi.RegisterLegacyStrictHandler(legacy, mux)
	r := httptest.NewRequest("POST", "/api/cex/tradeflow", strings.NewReader(`{"provider":"binance","market_side":"bid","btc_amount":0.001,"fiat_currency":"EUR","required_capital":100,"quote_id":"q"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("admission contract rejected: %d %s", w.Code, w.Body.String())
	}
}
