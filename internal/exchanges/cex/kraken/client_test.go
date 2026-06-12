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

func TestPrivateRequestsUseIncreasingNonces(t *testing.T) {
	nonces := make([]string, 0, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		for _, part := range strings.Split(string(body), "&") {
			if strings.HasPrefix(part, "nonce=") {
				nonces = append(nonces, strings.TrimPrefix(part, "nonce="))
			}
		}
		switch r.URL.Path {
		case "/0/private/DepositMethods":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  []string{},
				"result": []map[string]any{{"method": "Bitcoin", "limit": false, "gen-address": true}},
			})
		case "/0/private/DepositAddresses":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": []string{},
				"result": []map[string]string{{
					"address": "bc1qexample",
				}},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	if _, err := client.GetDepositMethods(context.Background(), "XBT"); err != nil {
		t.Fatalf("GetDepositMethods: %v", err)
	}
	if _, err := client.GetDepositAddresses(context.Background(), "XBT", "Bitcoin", false); err != nil {
		t.Fatalf("GetDepositAddresses: %v", err)
	}
	if len(nonces) != 2 {
		t.Fatalf("nonces = %v, want 2 entries", nonces)
	}
	if nonces[0] >= nonces[1] {
		t.Fatalf("nonces not increasing: %v", nonces)
	}
}

func TestPrivateRequestRetriesInvalidNonce(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  []string{"EAPI:Invalid nonce"},
				"result": map[string]string{},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  []string{},
			"result": map[string]string{"XXBT": "0.1"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key-retry", base64.StdEncoding.EncodeToString([]byte("secret")))
	balance, err := client.Balance(context.Background())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if balance["XXBT"] != "0.1" {
		t.Fatalf("XXBT balance = %q, want 0.1", balance["XXBT"])
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

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
