package binance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBinanceFuturesPlaceOrderShort(t *testing.T) {
	leverageCalled := false
	var gotOrderBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		switch r.URL.Path {
		case "/fapi/v1/leverage":
			leverageCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"symbol":           "BTCUSDT",
				"leverage":         3,
				"maxNotionalValue": "1000000",
			})
		case "/fapi/v1/order":
			gotOrderBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"orderId": int64(99887766),
				"status":  "NEW",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:   srv.URL,
		apiKey:    "testkey",
		secretKey: "secret",
		http:      &http.Client{},
	}

	res, err := client.PlaceOrder(context.Background(), "BTCUSDT", "short", 0.5, 3)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !leverageCalled {
		t.Fatal("leverage endpoint was not called")
	}
	if res.OrderID != "99887766" {
		t.Fatalf("order_id = %q, want 99887766", res.OrderID)
	}
	if res.Side != "short" {
		t.Fatalf("side = %q, want short", res.Side)
	}
	for _, want := range []string{"symbol=BTCUSDT", "side=SELL", "type=MARKET", "quantity=0.500"} {
		if !strings.Contains(gotOrderBody, want) {
			t.Fatalf("order body %q missing %q", gotOrderBody, want)
		}
	}
}

func TestBinanceFuturesPlaceOrderLongUsesBuy(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/fapi/v1/leverage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"symbol": "BTCUSDT", "leverage": 1, "maxNotionalValue": "1000000",
			})
		case "/fapi/v1/order":
			gotBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"orderId": int64(12345), "status": "NEW",
			})
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL: srv.URL, apiKey: "k", secretKey: "s", http: &http.Client{},
	}
	if _, err := client.PlaceOrder(context.Background(), "BTCUSDT", "long", 0.1, 1); err != nil {
		t.Fatalf("PlaceOrder long: %v", err)
	}
	if !strings.Contains(gotBody, "side=BUY") {
		t.Fatalf("expected side=BUY, got body: %s", gotBody)
	}
}

func TestBinanceFuturesLeverageFailureDoesNotAbortOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v1/leverage":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-4046,"msg":"No need to change leverage."}`))
		case "/fapi/v1/order":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"orderId": int64(55555), "status": "NEW",
			})
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL: srv.URL, apiKey: "k", secretKey: "s", http: &http.Client{},
	}
	res, err := client.PlaceOrder(context.Background(), "BTCUSDT", "short", 0.2, 5)
	if err != nil {
		t.Fatalf("expected order to succeed despite leverage failure, got: %v", err)
	}
	if res.OrderID != "55555" {
		t.Fatalf("order_id = %q, want 55555", res.OrderID)
	}
}
