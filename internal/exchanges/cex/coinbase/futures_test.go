package coinbase

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"context"
)

func TestCoinbaseFuturesPlaceOrderShort(t *testing.T) {
	var gotOrderBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/portfolios":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"portfolios": []map[string]any{
					{"portfolio_uuid": "uuid-abc"},
				},
			})
		case "/api/v1/orders":
			b := make([]byte, 4096)
			n, _ := r.Body.Read(b)
			gotOrderBody = string(b[:n])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"order": map[string]any{
					"order_id": "CB-ORDER-789",
					"status":   "OPEN",
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:    srv.URL,
		apiKey:     "testkey",
		apiSecret:  base64.StdEncoding.EncodeToString([]byte("secret")),
		passphrase: "pass",
		http:       &http.Client{},
	}

	res, err := client.PlaceOrder(context.Background(), "BTC-PERP", "short", 0.25)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if res.OrderID != "CB-ORDER-789" {
		t.Fatalf("order_id = %q, want CB-ORDER-789", res.OrderID)
	}
	if res.Side != "short" {
		t.Fatalf("side = %q, want short", res.Side)
	}
	// Body must contain portfolio UUID, SELL side, instrument, and IOC TIF.
	for _, want := range []string{"uuid-abc", `"SELL"`, "BTC-PERP", "IOC"} {
		if !strings.Contains(gotOrderBody, want) {
			t.Fatalf("order body %q missing %q", gotOrderBody, want)
		}
	}
}

func TestCoinbaseFuturesPlaceOrderLongUsesBuy(t *testing.T) {
	var gotSide string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/portfolios":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"portfolios": []map[string]any{{"portfolio_uuid": "uuid-xyz"}},
			})
		case "/api/v1/orders":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotSide, _ = body["side"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"order": map[string]any{"order_id": "X", "status": "OPEN"},
			})
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:    srv.URL,
		apiKey:     "k",
		apiSecret:  base64.StdEncoding.EncodeToString([]byte("s")),
		passphrase: "p",
		http:       &http.Client{},
	}
	if _, err := client.PlaceOrder(context.Background(), "BTC-PERP", "long", 0.1); err != nil {
		t.Fatalf("PlaceOrder long: %v", err)
	}
	if gotSide != "BUY" {
		t.Fatalf("side = %q, want BUY", gotSide)
	}
}

func TestCoinbaseFuturesNoPortfolioError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"portfolios": []any{}})
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:    srv.URL,
		apiKey:     "k",
		apiSecret:  base64.StdEncoding.EncodeToString([]byte("s")),
		passphrase: "p",
		http:       &http.Client{},
	}
	_, err := client.PlaceOrder(context.Background(), "BTC-PERP", "short", 0.1)
	if err == nil || !strings.Contains(err.Error(), "no portfolios") {
		t.Fatalf("expected no portfolios error, got %v", err)
	}
}
