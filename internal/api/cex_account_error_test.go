package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dexTakerController/internal/cexaccount"
)

func TestCEXProviderRequestReturnsSafeStructuredAccessError(t *testing.T) {
	s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, cexaccount.Config{}))
	req := httptest.NewRequest(http.MethodGet, "/api/cex/balances?provider=binance", nil)
	rec := httptest.NewRecorder()

	s.handleCexProviderRequest(rec, req, "balances", func(context.Context, string) (any, error) {
		return nil, errors.New(`binance: GET /api/v3/account returned HTTP 401: {"code":-2015}`)
	})

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "cex_api_access_failed" || body["provider"] != "Binance" || body["operation"] != "balances" {
		t.Fatalf("error response = %#v", body)
	}
	want := "Unable to load balances from Binance. Check that an active API key is configured, its IP allowlist includes this server, and it has the required permission."
	if body["error"] != want {
		t.Fatalf("error = %q, want %q", body["error"], want)
	}
}

func TestCEXProviderRequestReturnsEmptyBalancesAsSuccess(t *testing.T) {
	s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, cexaccount.Config{}))
	req := httptest.NewRequest(http.MethodGet, "/api/cex/balances?provider=coinbase", nil)
	rec := httptest.NewRecorder()

	s.handleCexProviderRequest(rec, req, "balances", func(context.Context, string) (any, error) {
		return &cexaccount.BalancesResponse{Fiat: []cexaccount.AssetBalance{}, Source: "coinbase"}, nil
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
