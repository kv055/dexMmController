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

func TestNewFuturesClientUsesSuppliedCredentials(t *testing.T) {
	client := NewFuturesClient(" db-key ", " db-secret ", " db-passphrase ")
	if client.apiKey != "db-key" || client.apiSecret != "db-secret" || client.passphrase != "db-passphrase" {
		t.Fatalf("credentials = %q/%q/%q, want supplied database credentials", client.apiKey, client.apiSecret, client.passphrase)
	}
}

func TestDecodeCoinbasePortfoliosAcceptsBareArrayAndWrapped(t *testing.T) {
	bare, err := decodeCoinbasePortfolios([]byte(`[{"portfolio_uuid":"uuid-1"},{"id":"uuid-2"}]`))
	if err != nil {
		t.Fatalf("bare array: %v", err)
	}
	if len(bare) != 2 || bare[0] != "uuid-1" || bare[1] != "uuid-2" {
		t.Fatalf("bare = %#v", bare)
	}

	wrapped, err := decodeCoinbasePortfolios([]byte(`{"portfolios":[{"portfolio_uuid":"uuid-3"}]}`))
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if len(wrapped) != 1 || wrapped[0] != "uuid-3" {
		t.Fatalf("wrapped = %#v", wrapped)
	}
}

func TestFuturesClientCheckAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/portfolios" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("CB-ACCESS-KEY") == "" || r.Header.Get("CB-ACCESS-PASSPHRASE") == "" {
			t.Fatalf("missing INTX auth headers")
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"portfolio_uuid": "uuid-ok"}})
	}))
	defer srv.Close()

	client := NewFuturesClientWithBaseURL(srv.URL, "key", base64.StdEncoding.EncodeToString([]byte("secret")), "pass")
	if err := client.CheckAccess(context.Background()); err != nil {
		t.Fatalf("CheckAccess: %v", err)
	}
}

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
