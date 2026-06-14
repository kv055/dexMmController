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

func TestMarketBuySubmitsOrder(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0/private/AddOrder" {
			t.Fatalf("path = %s, want /0/private/AddOrder", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": []string{},
			"result": map[string]any{
				"descr": map[string]string{"order": "buy 0.01000000 XBTEUR @ market"},
				"txid":  []string{"OABCDE-12345-FGHIJK"},
			},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	res, err := client.MarketBuy(context.Background(), "XBTEUR", 0.01)
	if err != nil {
		t.Fatalf("MarketBuy: %v", err)
	}
	if len(res.TxID) != 1 || res.TxID[0] != "OABCDE-12345-FGHIJK" {
		t.Fatalf("txid = %v, want one entry", res.TxID)
	}
	for _, want := range []string{"pair=XBTEUR", "type=buy", "ordertype=market", "volume=0.01"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %q missing %q", gotBody, want)
		}
	}
}

func TestMarketBuyReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  []string{"EOrder:Insufficient funds"},
			"result": map[string]any{},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	if _, err := client.MarketBuy(context.Background(), "XBTEUR", 0.01); err == nil {
		t.Fatalf("expected error for insufficient funds")
	}
}

func TestGetWithdrawMethodsParsesObjectFee(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0/private/WithdrawMethods" {
			t.Fatalf("path = %s, want /0/private/WithdrawMethods", r.URL.Path)
		}
		// Kraken returns `fee` as a nested object for some assets/methods.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": []string{},
			"result": []map[string]any{{
				"method": "Bitcoin",
				"limit":  false,
				"fee":    map[string]any{"fee": "0.00001", "currency": "XBT"},
			}},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	methods, err := client.GetWithdrawMethods(context.Background(), "XBT")
	if err != nil {
		t.Fatalf("GetWithdrawMethods: %v", err)
	}
	if len(methods) != 1 || methods[0].Method != "Bitcoin" {
		t.Fatalf("methods = %#v, want one Bitcoin method", methods)
	}
}

func TestValidateOrderPermissionSendsValidateFlag(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0/private/AddOrder" {
			t.Fatalf("path = %s, want /0/private/AddOrder", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  []string{},
			"result": map[string]any{"descr": map[string]string{"order": "buy 0.0001 XBTEUR @ market"}},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	if err := client.ValidateOrderPermission(context.Background(), "XBTEUR"); err != nil {
		t.Fatalf("ValidateOrderPermission: %v", err)
	}
	for _, want := range []string{"validate=true", "type=buy", "ordertype=market", "pair=XBTEUR"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %q missing %q", gotBody, want)
		}
	}
}

func TestValidateOrderPermissionReturnsPermissionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  []string{"EGeneral:Permission denied"},
			"result": map[string]any{},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "api-key", base64.StdEncoding.EncodeToString([]byte("secret")))
	err := client.ValidateOrderPermission(context.Background(), "XBTEUR")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err = %v, want Permission denied", err)
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
