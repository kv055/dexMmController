package coinbaseexchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddressBookParsesEntriesAndSigns(t *testing.T) {
	const secret = "dGVzdC1zZWNyZXQta2V5LWZvci1obWFjLXNpZ25pbmc=" // base64 of arbitrary bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/address-book" {
			t.Fatalf("path = %s, want /address-book", r.URL.Path)
		}
		if got := r.Header.Get("CB-ACCESS-KEY"); got != "key-1" {
			t.Fatalf("CB-ACCESS-KEY = %q, want key-1", got)
		}
		if got := r.Header.Get("CB-ACCESS-PASSPHRASE"); got != "pass-1" {
			t.Fatalf("CB-ACCESS-PASSPHRASE = %q, want pass-1", got)
		}
		ts := r.Header.Get("CB-ACCESS-TIMESTAMP")
		if ts == "" {
			t.Fatalf("missing CB-ACCESS-TIMESTAMP")
		}
		want, err := sign(ts, http.MethodGet, "/address-book", secret)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if got := r.Header.Get("CB-ACCESS-SIGN"); got != want {
			t.Fatalf("CB-ACCESS-SIGN = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`[
			{"id":"a1","address":"bc1qexample","currency":"BTC","label":"cold storage","network":"bitcoin"},
			{"id":"a2","address":"0xexample","currency":"ETH","label":"eth","network":"ethereum"}
		]`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "key-1", secret, "pass-1")
	entries, err := client.AddressBook(context.Background())
	if err != nil {
		t.Fatalf("AddressBook: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Address != "bc1qexample" || entries[0].Currency != "BTC" || entries[0].Label != "cold storage" {
		t.Fatalf("entries[0] = %#v", entries[0])
	}
}

func TestSignMatchesReferenceHMAC(t *testing.T) {
	const secret = "dGVzdC1zZWNyZXQ=" // base64("test-secret")
	got, err := sign("1700000000", http.MethodGet, "/address-book", secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	secretBytes, _ := base64.StdEncoding.DecodeString(secret)
	mac := hmac.New(sha256.New, secretBytes)
	_, _ = mac.Write([]byte("1700000000" + "GET" + "/address-book"))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("sign = %q, want %q", got, want)
	}
}
