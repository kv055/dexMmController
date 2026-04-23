package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
