package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"commons/cexclient"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestAccountInfoSignsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/account" {
			t.Fatalf("path = %s, want /api/v3/account", r.URL.Path)
		}
		if r.Header.Get("X-MBX-APIKEY") != "api-key" {
			t.Fatalf("missing API key header")
		}
		if r.URL.Query().Get("timestamp") == "" || r.URL.Query().Get("signature") == "" {
			t.Fatalf("missing signed query params: %s", r.URL.RawQuery)
		}
		writeJSON(w, http.StatusOK, map[string]any{"canTrade": true, "canWithdraw": true, "canDeposit": true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	info, err := client.AccountInfo(context.Background())
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if !info.CanTrade {
		t.Fatal("CanTrade = false, want true")
	}
}

func TestAccountInfoDoesNotRetryTimestampError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/account" {
			t.Fatalf("path = %s, want /api/v3/account", r.URL.Path)
		}
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1021,"msg":"Timestamp for this request was 1000ms ahead of the server's time."}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	_, err := client.AccountInfo(context.Background())
	if err == nil {
		t.Fatal("expected timestamp error")
	}
	got, ok := cexclient.AsError(err)
	if !ok {
		t.Fatalf("AsError failed: %v", err)
	}
	if got.Code != "-1021" || got.HTTPStatus != 400 {
		t.Fatalf("got %#v", got)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no auto-retry)", attempts)
	}
}

func TestMarketBuyPostsOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/order" {
			t.Fatalf("path = %s, want /api/v3/order", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("X-MBX-APIKEY") != "api-key" {
			t.Fatalf("missing API key header")
		}
		q := r.URL.Query()
		if q.Get("symbol") != "BTCEUR" || q.Get("side") != "BUY" || q.Get("type") != "MARKET" {
			t.Fatalf("unexpected order params: %s", r.URL.RawQuery)
		}
		qty, err := strconv.ParseFloat(q.Get("quantity"), 64)
		if err != nil || qty < 0.0099 || qty > 0.0101 {
			t.Fatalf("quantity = %q", q.Get("quantity"))
		}
		if q.Get("signature") == "" || q.Get("timestamp") == "" {
			t.Fatalf("missing signature/timestamp: %s", r.URL.RawQuery)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"symbol": "BTCEUR", "orderId": 12345, "status": "FILLED",
			"executedQty": "0.01000000", "cummulativeQuoteQty": "600.00",
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	order, err := client.MarketBuy(context.Background(), "BTCEUR", 0.01)
	if err != nil {
		t.Fatalf("MarketBuy: %v", err)
	}
	if order.OrderID != 12345 || order.Status != "FILLED" {
		t.Fatalf("order = %+v, want id 12345 FILLED", order)
	}
}

func TestKeyRestrictionsSignsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sapi/v1/account/apiRestrictions" {
			t.Fatalf("path = %s, want /sapi/v1/account/apiRestrictions", r.URL.Path)
		}
		if r.Header.Get("X-MBX-APIKEY") != "api-key" {
			t.Fatalf("missing API key header")
		}
		if r.URL.Query().Get("timestamp") == "" || r.URL.Query().Get("signature") == "" {
			t.Fatalf("missing signed query params: %s", r.URL.RawQuery)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ipRestrict":                 false,
			"enableReading":              true,
			"enableSpotAndMarginTrading": true,
			"enableWithdrawals":          false,
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	restrictions, err := client.KeyRestrictions(context.Background())
	if err != nil {
		t.Fatalf("KeyRestrictions: %v", err)
	}
	if !restrictions.EnableReading || !restrictions.EnableSpotAndMarginTrading || restrictions.EnableWithdrawals {
		t.Fatalf("restrictions = %+v, want read+trade granted and withdraw denied", restrictions)
	}
}

func TestKeyRestrictionsParsesFutures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"enableReading":              true,
			"enableSpotAndMarginTrading": true,
			"enableWithdrawals":          false,
			"enableFutures":              true,
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	restrictions, err := client.KeyRestrictions(context.Background())
	if err != nil {
		t.Fatalf("KeyRestrictions: %v", err)
	}
	if !restrictions.EnableFutures {
		t.Fatalf("EnableFutures = false, want true (restrictions = %+v)", restrictions)
	}
}
