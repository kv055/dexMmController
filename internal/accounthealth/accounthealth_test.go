package accounthealth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/dex/robosats"
)

type fakeRepo struct {
	accountProvider   string
	keys              []db.APIKey
	updatedProvider   string
	updatedHealthy    bool
	checkedAt         time.Time
	permissionUpdates []struct {
		keyID       uint
		permissions map[string]string
		checkedAt   time.Time
	}
}

func (f *fakeRepo) GetAccountByProvider(_ context.Context, provider string) (*db.Account, error) {
	if f.accountProvider != provider {
		return nil, errors.New("not found")
	}
	return &db.Account{Provider: provider}, nil
}

func (f *fakeRepo) GetActiveAPIKeysForProvider(context.Context, string) ([]db.APIKey, error) {
	return f.keys, nil
}

func (f *fakeRepo) UpdateAPIKeyPermissions(_ context.Context, keyID uint, permissions map[string]string, checkedAt time.Time) error {
	f.permissionUpdates = append(f.permissionUpdates, struct {
		keyID       uint
		permissions map[string]string
		checkedAt   time.Time
	}{keyID, permissions, checkedAt})
	return nil
}

func (f *fakeRepo) UpdateAccountHealthCheckedAt(_ context.Context, provider string, healthy bool) (time.Time, error) {
	f.updatedProvider = provider
	f.updatedHealthy = healthy
	if f.checkedAt.IsZero() {
		f.checkedAt = time.Date(2026, 4, 23, 1, 2, 3, 0, time.UTC)
	}
	return f.checkedAt, nil
}

func TestCanonicalProvider(t *testing.T) {
	tests := map[string]string{
		"kraken":    "Kraken",
		"BINANCE":   "Binance",
		"coinbase":  "Coinbase",
		"hodl hodl": "HodlHodl",
		"HodlHodl":  "HodlHodl",
		"robosats":  "Robosats",
		"RoboSats":  "Robosats",
	}
	for input, want := range tests {
		got, err := CanonicalProvider(input)
		if err != nil {
			t.Fatalf("CanonicalProvider(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("CanonicalProvider(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := CanonicalProvider("Bisq"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("CanonicalProvider(Bisq) error = %v, want ErrUnsupportedProvider", err)
	}
}

func TestParseIntervalDefaultIsTenMinutes(t *testing.T) {
	for _, raw := range []string{"", "nope", "-1s", "0s"} {
		if got := ParseInterval(raw); got != 10*time.Minute {
			t.Fatalf("ParseInterval(%q) = %s, want 10m", raw, got)
		}
	}
	if got := ParseInterval("30s"); got != 30*time.Second {
		t.Fatalf("ParseInterval(30s) = %s, want 30s", got)
	}
}

func TestCheckProviderNoActiveKeysMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{accountProvider: "Kraken"}
	checker := NewChecker(repo, Config{})
	result, err := checker.CheckProvider(context.Background(), "kraken")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
	if repo.updatedProvider != "Kraken" || repo.updatedHealthy {
		t.Fatalf("updated provider=%q healthy=%v, want Kraken false", repo.updatedProvider, repo.updatedHealthy)
	}
}

func TestCheckProviderMissingSecretMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{
		accountProvider: "Binance",
		keys:            []db.APIKey{{ID: 7, APIKey: "key", IsActive: true}},
	}
	checker := NewChecker(repo, Config{})
	result, err := checker.CheckProvider(context.Background(), "binance")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
	if repo.updatedHealthy {
		t.Fatal("updated healthy = true, want false")
	}
}

func TestCheckProviderRobosatsNoActiveTokenMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{accountProvider: "Robosats"}
	checker := NewChecker(repo, Config{})
	result, err := checker.CheckProvider(context.Background(), "robosats")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
	if repo.updatedProvider != "Robosats" || repo.updatedHealthy {
		t.Fatalf("updated provider=%q healthy=%v, want Robosats false", repo.updatedProvider, repo.updatedHealthy)
	}
}

func TestCheckProviderRobosatsEmptyTokenMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{
		accountProvider: "Robosats",
		keys:            []db.APIKey{{ID: 7, IsActive: true}},
	}
	checker := NewChecker(repo, Config{})
	result, err := checker.CheckProvider(context.Background(), "robosats")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
}

func TestCheckProviderRobosatsDoesNotRequireSecret(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nickname":"testbot"}`))
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Robosats",
		keys:            []db.APIKey{{ID: 7, APIKey: "robot-token", IsActive: true}},
	}
	checker := NewChecker(repo, Config{RobosatsBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "robosats")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "healthy" || !result.Healthy {
		t.Fatalf("result = %+v, want healthy true", result)
	}
	if want := "Token " + robosats.AuthHeaderToken("robot-token") + " | Public "; !strings.HasPrefix(gotAuth, want) {
		t.Fatalf("Authorization = %q, want prefix %q", gotAuth, want)
	}
	if repo.updatedProvider != "Robosats" || !repo.updatedHealthy {
		t.Fatalf("updated provider=%q healthy=%v, want Robosats true", repo.updatedProvider, repo.updatedHealthy)
	}
	if len(result.Keys) != 1 || result.Keys[0].Permissions["robot"] != PermGranted {
		t.Fatalf("result keys = %+v, want robot permission", result.Keys)
	}
	if len(repo.permissionUpdates) != 1 || repo.permissionUpdates[0].permissions["robot"] != PermGranted {
		t.Fatalf("permission updates = %+v, want robot permission persisted", repo.permissionUpdates)
	}
}

func TestCheckProviderHodlHodlValidatesAPIAndSignatureKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/me":
			_, _ = w.Write([]byte(`{"status":"success","user":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/contracts/verify_signature_key":
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "HodlHodl",
		keys: []db.APIKey{{
			ID:        9,
			APIKey:    "hh-api-key",
			APISecret: "hh-signature-key",
			IsActive:  true,
		}},
	}
	checker := NewChecker(repo, Config{HodlHodlBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "hodlhodl")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "healthy" || !result.Healthy {
		t.Fatalf("result = %+v, want healthy true", result)
	}
	if len(result.Keys) != 1 {
		t.Fatalf("result keys = %+v, want one key result", result.Keys)
	}
	if result.Keys[0].Permissions["api_key"] != PermGranted || result.Keys[0].Permissions["signature_key"] != PermGranted {
		t.Fatalf("permissions = %+v, want api_key and signature_key", result.Keys[0].Permissions)
	}
	if len(repo.permissionUpdates) != 1 {
		t.Fatalf("permission updates = %+v, want one persisted update", repo.permissionUpdates)
	}
}

func TestCheckProviderHodlHodlMissingSignatureMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{
		accountProvider: "HodlHodl",
		keys:            []db.APIKey{{ID: 9, APIKey: "hh-api-key", IsActive: true}},
	}
	checker := NewChecker(repo, Config{})
	result, err := checker.CheckProvider(context.Background(), "hodlhodl")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
}

func TestCheckProviderBinanceCanonicalPermissions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/time" {
			_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": time.Now().UnixMilli()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enableReading":              true,
			"enableSpotAndMarginTrading": true,
			"enableWithdrawals":          false,
			"enableFutures":              true,
		})
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Binance",
		keys:            []db.APIKey{{ID: 3, APIKey: "k", APISecret: "s", IsActive: true}},
	}
	checker := NewChecker(repo, Config{BinanceBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "binance")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if !result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want healthy with one key", result)
	}
	perms := result.Keys[0].Permissions
	if perms[PermSpotTrading] != PermGranted || perms[PermReadBalances] != PermGranted ||
		perms[PermWithdraw] != PermDenied || perms[PermHedgePositions] != PermGranted {
		t.Fatalf("permissions = %+v", perms)
	}
}

func TestCheckProviderKrakenProbesPermissions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/0/private/Balance":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": map[string]string{"XXBT": "0.1"}})
		case "/0/private/AddOrder": // validate-only spot probe — key lacks order permission
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EGeneral:Permission denied"}, "result": map[string]any{}})
		case "/0/private/WithdrawMethods":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": []map[string]any{{"method": "Bitcoin"}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Kraken",
		keys:            []db.APIKey{{ID: 5, APIKey: "k", APISecret: base64.StdEncoding.EncodeToString([]byte("secret")), IsActive: true}},
	}
	checker := NewChecker(repo, Config{KrakenBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "kraken")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if !result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want healthy with one key", result)
	}
	perms := result.Keys[0].Permissions
	if perms[PermReadBalances] != PermGranted || perms[PermSpotTrading] != PermDenied ||
		perms[PermWithdraw] != PermGranted || perms[PermHedgePositions] != PermUnsupported {
		t.Fatalf("permissions = %+v", perms)
	}
}

func TestCheckProviderCoinbaseHedgeUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/brokerage/key_permissions" {
			t.Fatalf("path = %s, want key_permissions", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"can_view":true,"can_trade":true,"can_transfer":false}`))
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Coinbase",
		keys:            []db.APIKey{{ID: 4, APIKey: "organizations/org/apiKeys/key", APISecret: testECPrivateKeyPEM(t), IsActive: true}},
	}
	checker := NewChecker(repo, Config{CoinbaseBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "coinbase")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if !result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want healthy with one key", result)
	}
	perms := result.Keys[0].Permissions
	if perms[PermSpotTrading] != PermGranted || perms[PermReadBalances] != PermGranted ||
		perms[PermWithdraw] != PermDenied || perms[PermHedgePositions] != PermUnsupported {
		t.Fatalf("permissions = %+v", perms)
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

func TestCheckProviderRobosatsBadStatusMarksUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Robosats",
		keys:            []db.APIKey{{ID: 7, APIKey: "robot-token", IsActive: true}},
	}
	checker := NewChecker(repo, Config{RobosatsBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "robosats")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
}
