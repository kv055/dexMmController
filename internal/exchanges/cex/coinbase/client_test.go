package coinbase

import (
	"context"
	"encoding/json"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKeyPermissionsUsesJWTBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/brokerage/key_permissions" {
			t.Fatalf("path = %s, want key_permissions", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Fatalf("missing bearer token")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"can_view":true,"can_trade":true,"can_transfer":false}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "organizations/org/apiKeys/key", testECPrivateKeyPEM(t))
	permissions, err := client.KeyPermissions(context.Background())
	if err != nil {
		t.Fatalf("KeyPermissions: %v", err)
	}
	if !permissions.CanView || !permissions.CanTrade || permissions.CanTransfer {
		t.Fatalf("permissions = %+v, want view+trade granted and transfer denied", permissions)
	}
}

func TestListAccountsPaginates(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v3/brokerage/accounts" {
			t.Fatalf("path = %s, want /api/v3/brokerage/accounts", r.URL.Path)
		}
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{{
					"uuid":     "btc-account",
					"currency": "BTC",
					"active":   true,
					"available_balance": map[string]string{
						"value":    "0.5",
						"currency": "BTC",
					},
					"hold": map[string]string{
						"value":    "0",
						"currency": "BTC",
					},
				}},
				"has_next": true,
				"cursor":   "next-page",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accounts": []map[string]any{{
				"uuid":     "eur-account",
				"currency": "EUR",
				"active":   true,
				"available_balance": map[string]string{
					"value":    "100",
					"currency": "EUR",
				},
				"hold": map[string]string{
					"value":    "0",
					"currency": "EUR",
				},
			}},
			"has_next": false,
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "organizations/org/apiKeys/key", testECPrivateKeyPEM(t))
	accounts, err := client.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(accounts))
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestBTCDepositAddressesUsesExistingAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/brokerage/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{{
					"uuid":     "btc-account",
					"currency": "BTC",
					"active":   true,
				}},
				"has_next": false,
			})
		case r.URL.Path == "/v2/accounts/btc-account/addresses" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{
					"id":      "addr-1",
					"address": "bc1qexample",
					"network": "bitcoin",
				}},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "organizations/org/apiKeys/key", testECPrivateKeyPEM(t))
	addresses, err := client.BTCDepositAddresses(context.Background())
	if err != nil {
		t.Fatalf("BTCDepositAddresses: %v", err)
	}
	if len(addresses) != 1 || addresses[0].Address != "bc1qexample" {
		t.Fatalf("addresses = %#v, want bc1qexample", addresses)
	}
}

func TestBuildJWTIncludesThreeParts(t *testing.T) {
	client := NewClient("", "organizations/org/apiKeys/key", testECPrivateKeyPEM(t))
	token, err := client.buildJWT(http.MethodGet, "/api/v3/brokerage/key_permissions")
	if err != nil {
		t.Fatalf("buildJWT: %v", err)
	}
	if parts := strings.Split(token, "."); len(parts) != 3 {
		t.Fatalf("jwt parts = %d, want 3", len(parts))
	}
}

func testECPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw}))
}
