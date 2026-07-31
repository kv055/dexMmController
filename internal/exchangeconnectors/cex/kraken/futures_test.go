package kraken

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKrakenFuturesCheckAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/v1/api-keys/v3/check" {
			t.Fatalf("path = %s, want /api/auth/v1/api-keys/v3/check", r.URL.Path)
		}
		if r.Header.Get("APIKey") == "" || r.Header.Get("Authent") == "" {
			t.Fatal("expected futures auth headers")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiKey":     "futures-key",
			"accountUid": "uid-1",
			"permissions": map[string]string{
				"general":  "FULL_ACCESS",
				"transfer": "READ_ONLY",
			},
		})
	}))
	defer srv.Close()

	client := NewFuturesClientWithBaseURL(srv.URL, "futures-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	info, err := client.CheckAPIKey(context.Background())
	if err != nil {
		t.Fatalf("CheckAPIKey: %v", err)
	}
	if info.Permissions.General != FuturesAccessFull || info.Permissions.Transfer != FuturesAccessReadOnly {
		t.Fatalf("permissions = %+v", info.Permissions)
	}
}

func TestNewFuturesClientUsesSuppliedCredentials(t *testing.T) {
	client := NewFuturesClient(" db-key ", " db-secret ")
	if client.apiKey != "db-key" || client.apiSecret != "db-secret" {
		t.Fatalf("credentials = %q/%q, want supplied database credentials", client.apiKey, client.apiSecret)
	}
}

func TestKrakenFuturesPlaceOrderShort(t *testing.T) {
	var gotOrderBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/derivatives/api/v3/tickers/PI_XBTUSD":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tickers": []map[string]any{
					{"tag": "perpetual", "markPrice": 60000.0},
				},
			})
		case "/derivatives/api/v3/sendorder":
			b, _ := io.ReadAll(r.Body)
			gotOrderBody = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": "success",
				"sendStatus": map[string]any{
					"order_id": "ORDER-123",
					"status":   "placed",
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:   srv.URL,
		apiKey:    "testkey",
		apiSecret: base64.StdEncoding.EncodeToString([]byte("secret")),
		http:      &http.Client{},
	}

	res, err := client.PlaceOrder(context.Background(), "PI_XBTUSD", "short", 0.5)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if res.OrderID != "ORDER-123" {
		t.Fatalf("order_id = %q, want ORDER-123", res.OrderID)
	}
	if res.Side != "short" {
		t.Fatalf("side = %q, want short", res.Side)
	}
	// 0.5 BTC * 60000 USD = 30000 contracts
	for _, want := range []string{"side=sell", "size=30000", "orderType=mkt", "symbol=PI_XBTUSD"} {
		if !strings.Contains(gotOrderBody, want) {
			t.Fatalf("order body %q missing %q", gotOrderBody, want)
		}
	}
}

func TestKrakenFuturesPlaceOrderLong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/derivatives/api/v3/tickers/PI_XBTUSD":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tickers": []map[string]any{{"markPrice": 50000.0}},
			})
		case "/derivatives/api/v3/sendorder":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "side=buy") {
				t.Fatalf("expected side=buy for long, got body: %s", b)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result":     "success",
				"sendStatus": map[string]any{"order_id": "ORDER-456", "status": "placed"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:   srv.URL,
		apiKey:    "testkey",
		apiSecret: base64.StdEncoding.EncodeToString([]byte("secret")),
		http:      &http.Client{},
	}
	if _, err := client.PlaceOrder(context.Background(), "PI_XBTUSD", "long", 0.1); err != nil {
		t.Fatalf("PlaceOrder long: %v", err)
	}
}

func TestKrakenFuturesPlaceOrderAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/derivatives/api/v3/tickers/PI_XBTUSD":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tickers": []map[string]any{{"markPrice": 60000.0}},
			})
		case "/derivatives/api/v3/sendorder":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": "error",
				"error":  "Insufficient funds",
				"sendStatus": map[string]any{
					"order_id": "",
					"status":   "rejected",
				},
			})
		}
	}))
	defer srv.Close()

	client := &FuturesClient{
		baseURL:   srv.URL,
		apiKey:    "testkey",
		apiSecret: base64.StdEncoding.EncodeToString([]byte("secret")),
		http:      &http.Client{},
	}
	_, err := client.PlaceOrder(context.Background(), "PI_XBTUSD", "short", 0.5)
	if err == nil || !strings.Contains(err.Error(), "Insufficient funds") {
		t.Fatalf("expected Insufficient funds error, got %v", err)
	}
}
