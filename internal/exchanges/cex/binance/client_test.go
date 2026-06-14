package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccountInfoSignsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		if r.URL.Path != "/api/v3/account" {
			t.Fatalf("path = %s, want /api/v3/account", r.URL.Path)
		}
		if r.Header.Get("X-MBX-APIKEY") != "api-key" {
			t.Fatalf("missing API key header")
		}
		if r.URL.Query().Get("timestamp") == "" || r.URL.Query().Get("signature") == "" {
			t.Fatalf("missing signed query params: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"canTrade": true, "canWithdraw": true, "canDeposit": true})
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

func TestSignedRequestRetriesAfterTimestampError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		if r.URL.Path != "/api/v3/account" {
			t.Fatalf("path = %s, want /api/v3/account", r.URL.Path)
		}
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-1021,"msg":"Timestamp for this request was 1000ms ahead of the server's time."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"canTrade": true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", "secret")
	if _, err := client.AccountInfo(context.Background()); err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestMarketBuyPostsOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		if r.URL.Path != "/api/v3/order" {
			t.Fatalf("path = %s, want /api/v3/order", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		_ = r.ParseForm()
		if r.PostForm.Get("symbol") != "BTCEUR" || r.PostForm.Get("side") != "BUY" ||
			r.PostForm.Get("type") != "MARKET" || r.PostForm.Get("quantity") != "0.01" {
			t.Fatalf("unexpected order params: %s", r.PostForm.Encode())
		}
		if r.PostForm.Get("signature") == "" {
			t.Fatalf("missing signature")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
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
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		if r.URL.Path != "/sapi/v1/account/apiRestrictions" {
			t.Fatalf("path = %s, want /sapi/v1/account/apiRestrictions", r.URL.Path)
		}
		if r.Header.Get("X-MBX-APIKEY") != "api-key" {
			t.Fatalf("missing API key header")
		}
		if r.URL.Query().Get("timestamp") == "" || r.URL.Query().Get("signature") == "" {
			t.Fatalf("missing signed query params: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
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
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
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
