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

func TestBalanceSignsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0/private/Balance" {
			t.Fatalf("path = %s, want /0/private/Balance", r.URL.Path)
		}
		if r.Header.Get("API-Key") != "api-key" || r.Header.Get("API-Sign") == "" {
			t.Fatalf("missing Kraken auth headers")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "nonce=") {
			t.Fatalf("missing nonce in body: %s", string(body))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": map[string]string{"XXBT": "0.1"}})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	balance, err := client.Balance(context.Background())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if balance["XXBT"] != "0.1" {
		t.Fatalf("XXBT balance = %q, want 0.1", balance["XXBT"])
	}
}
