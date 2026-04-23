package coinbase

import (
	"context"
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
		_, _ = w.Write([]byte(`{"permissions":[]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "organizations/org/apiKeys/key", testECPrivateKeyPEM(t))
	if err := client.KeyPermissions(context.Background()); err != nil {
		t.Fatalf("KeyPermissions: %v", err)
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
