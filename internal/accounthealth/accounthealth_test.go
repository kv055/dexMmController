package accounthealth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dexTakerController/internal/accounthealth"
	"dexTakerController/internal/cexaccount"
	"dexTakerController/internal/db"
	"dexTakerController/internal/exchangeconnectors/dex/robosats"
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
		return nil, fmt.Errorf("not found")
	}
	return &db.Account{Provider: provider}, nil
}

func (f *fakeRepo) GetActiveAPIKeysForProvider(context.Context, string) ([]db.APIKey, error) {
	return f.keys, nil
}

func (f *fakeRepo) UpdateAPIKeyPermissions(_ context.Context, keyID uint, permissions map[string]string, _ string, checkedAt time.Time) error {
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

func TestCheckProviderNoActiveKeysMarksUnhealthy(t *testing.T) {
	repo := &fakeRepo{accountProvider: "Kraken"}
	checker := accounthealth.NewChecker(repo, cexaccount.Config{})
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{})
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{})
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{})
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{RobosatsBaseURL: srv.URL})
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
	if len(result.Keys) != 1 || result.Keys[0].Permissions["robot"] != cexaccount.PermGranted {
		t.Fatalf("result keys = %+v, want robot permission", result.Keys)
	}
	if len(repo.permissionUpdates) != 1 || repo.permissionUpdates[0].permissions["robot"] != cexaccount.PermGranted {
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{HodlHodlBaseURL: srv.URL})
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
	if result.Keys[0].Permissions["api_key"] != cexaccount.PermGranted || result.Keys[0].Permissions["signature_key"] != cexaccount.PermGranted {
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{})
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
		w.Header().Set("Content-Type", "application/json")
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{BinanceBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "binance")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want unhealthy with one key", result)
	}
	perms := result.Keys[0].Permissions
	if perms[cexaccount.PermFetchDepositAddress] != cexaccount.PermGranted ||
		perms[cexaccount.PermSpotTrading] != cexaccount.PermGranted || perms[cexaccount.PermReadBalances] != cexaccount.PermGranted ||
		perms[cexaccount.PermWithdraw] != cexaccount.PermDenied || perms[cexaccount.PermHedgePositions] != cexaccount.PermGranted {
		t.Fatalf("permissions = %+v", perms)
	}
	if !strings.Contains(result.Message, "withdraw (denied)") {
		t.Fatalf("message = %q, want missing withdrawal detail", result.Message)
	}
}

func TestCheckProviderKrakenProbesPermissions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/0/private/DepositMethods":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": []map[string]any{{"method": "Bitcoin"}}})
		case "/0/private/Balance":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": map[string]string{"XXBT": "0.1"}})
		case "/0/private/AddOrder":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EGeneral:Permission denied"}, "result": map[string]any{}})
		case "/0/private/WithdrawMethods":
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": []map[string]any{{"method": "Bitcoin"}}})
		case "/api/auth/v1/api-keys/v3/check":
			http.Error(w, `{"error":"authenticationError"}`, http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Kraken",
		keys:            []db.APIKey{{ID: 5, APIKey: "k", APISecret: base64.StdEncoding.EncodeToString([]byte("secret")), IsActive: true}},
	}
	checker := accounthealth.NewChecker(repo, cexaccount.Config{KrakenBaseURL: srv.URL, KrakenFuturesBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "kraken")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want unhealthy with one key", result)
	}
	perms := result.Keys[0].Permissions
	if perms[cexaccount.PermFetchDepositAddress] != cexaccount.PermGranted ||
		perms[cexaccount.PermReadBalances] != cexaccount.PermGranted || perms[cexaccount.PermSpotTrading] != cexaccount.PermDenied ||
		perms[cexaccount.PermWithdraw] != cexaccount.PermGranted || perms[cexaccount.PermHedgePositions] != cexaccount.PermDenied {
		t.Fatalf("permissions = %+v", perms)
	}
}

func TestCheckProviderKrakenCombinesSpotAndFuturesKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("API-Key")
		if apiKey == "" {
			apiKey = r.Header.Get("APIKey")
		}
		switch r.URL.Path {
		case "/0/private/DepositMethods":
			if apiKey != "spot-key" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EAPI:Invalid key"}, "result": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": []map[string]any{{"method": "Bitcoin"}}})
		case "/0/private/Balance":
			if apiKey != "spot-key" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EAPI:Invalid key"}, "result": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": map[string]string{"XXBT": "0.1"}})
		case "/0/private/WithdrawMethods":
			if apiKey != "spot-key" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EAPI:Invalid key"}, "result": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": []map[string]any{{"method": "Bitcoin"}}})
		case "/0/private/AddOrder":
			if apiKey != "spot-key" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{"EAPI:Invalid key"}, "result": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": []string{}, "result": map[string]any{"descr": map[string]string{"order": "ok"}}})
		case "/api/auth/v1/api-keys/v3/check":
			if apiKey != "futures-key" {
				http.Error(w, `{"error":"authenticationError"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiKey":     "futures-key",
				"accountUid": "uid",
				"permissions": map[string]string{
					"general":  "FULL_ACCESS",
					"transfer": "NO_ACCESS",
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	secret := base64.StdEncoding.EncodeToString([]byte("secret"))
	repo := &fakeRepo{accountProvider: "Kraken", keys: []db.APIKey{
		{ID: 1, Name: "spot", APIKey: "spot-key", APISecret: secret, IsActive: true},
		{ID: 2, Name: "futures", APIKey: "futures-key", APISecret: secret, IsActive: true},
	}}
	result, err := accounthealth.NewChecker(repo, cexaccount.Config{
		KrakenBaseURL:        srv.URL,
		KrakenFuturesBaseURL: srv.URL,
	}).CheckProvider(context.Background(), "kraken")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || len(result.Keys) != 2 {
		t.Fatalf("result = %+v, want healthy combined spot+futures", result)
	}
	spot := result.Keys[0].Permissions
	if spot[cexaccount.PermFetchDepositAddress] != cexaccount.PermGranted ||
		spot[cexaccount.PermSpotTrading] != cexaccount.PermGranted ||
		spot[cexaccount.PermHedgePositions] != cexaccount.PermDenied {
		t.Fatalf("spot key permissions = %+v", spot)
	}
	futures := result.Keys[1].Permissions
	if futures[cexaccount.PermHedgePositions] != cexaccount.PermGranted ||
		futures[cexaccount.PermSpotTrading] != cexaccount.PermDenied {
		t.Fatalf("futures key permissions = %+v", futures)
	}
}

func TestCheckProviderCoinbaseHedgeDeniedWithoutINTXKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/brokerage/key_permissions":
			_, _ = w.Write([]byte(`{"can_view":true,"can_trade":true,"can_transfer":true}`))
		case "/api/v3/brokerage/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{{
					"uuid":     "eur-account",
					"currency": "EUR",
					"active":   true,
					"available_balance": map[string]string{
						"value":    "0",
						"currency": "EUR",
					},
				}},
				"has_next": false,
			})
		case "/v2/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "pagination": map[string]any{}})
		case "/v2/accounts/BTC":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{
		accountProvider: "Coinbase",
		keys:            []db.APIKey{{ID: 4, APIKey: "organizations/org/apiKeys/key", APISecret: testECPrivateKeyPEM(t), IsActive: true}}, // gitleaks:allow -- test-only placeholder
	}
	checker := accounthealth.NewChecker(repo, cexaccount.Config{CoinbaseBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "coinbase")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Healthy || len(result.Keys) != 1 {
		t.Fatalf("result = %+v, want unhealthy with one key (missing hedge)", result)
	}
	perms := result.Keys[0].Permissions
	if perms[cexaccount.PermFetchDepositAddress] != cexaccount.PermGranted ||
		perms[cexaccount.PermSpotTrading] != cexaccount.PermGranted || perms[cexaccount.PermReadBalances] != cexaccount.PermGranted ||
		perms[cexaccount.PermWithdraw] != cexaccount.PermGranted || perms[cexaccount.PermHedgePositions] != cexaccount.PermDenied {
		t.Fatalf("permissions = %+v", perms)
	}
	if !strings.Contains(result.Message, "International Exchange") {
		t.Fatalf("message = %q, want INTX guidance", result.Message)
	}
}

func TestCheckProviderCoinbaseCombinesSpotAndINTXKeys(t *testing.T) {
	spotSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/brokerage/key_permissions":
			_, _ = w.Write([]byte(`{"can_view":true,"can_trade":true,"can_transfer":true}`))
		case "/api/v3/brokerage/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{{
					"uuid":              "eur-account",
					"currency":          "EUR",
					"active":            true,
					"available_balance": map[string]string{"value": "10", "currency": "EUR"},
				}},
				"has_next": false,
			})
		case "/v2/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "pagination": map[string]any{}})
		case "/v2/accounts/BTC":
			http.NotFound(w, r)
		default:
			// INTX passphrase keys hit the spot base URL for CDP probes and fail soft.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	t.Cleanup(spotSrv.Close)

	intxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/portfolios" {
			t.Fatalf("intx path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"portfolio_uuid": "intx-portfolio"}})
	}))
	t.Cleanup(intxSrv.Close)

	repo := &fakeRepo{
		accountProvider: "Coinbase",
		keys: []db.APIKey{
			{ID: 1, Name: "spot", APIKey: "organizations/org/apiKeys/spot", APISecret: testECPrivateKeyPEM(t), IsActive: true},                                      // gitleaks:allow
			{ID: 2, Name: "intx", APIKey: "intx-key", APISecret: base64.StdEncoding.EncodeToString([]byte("intx-secret")), Passphrase: "intx-pass", IsActive: true}, // gitleaks:allow
		},
	}
	checker := accounthealth.NewChecker(repo, cexaccount.Config{
		CoinbaseBaseURL:     spotSrv.URL,
		CoinbaseINTXBaseURL: intxSrv.URL,
	})
	result, err := checker.CheckProvider(context.Background(), "coinbase")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if !result.Healthy || len(result.Keys) != 2 {
		t.Fatalf("result = %+v, want healthy with spot+intx keys", result)
	}
	spot := result.Keys[0].Permissions
	intx := result.Keys[1].Permissions
	if spot[cexaccount.PermReadBalances] != cexaccount.PermGranted || spot[cexaccount.PermHedgePositions] != cexaccount.PermDenied {
		t.Fatalf("spot permissions = %+v", spot)
	}
	if intx[cexaccount.PermHedgePositions] != cexaccount.PermGranted {
		t.Fatalf("intx permissions = %+v", intx)
	}
}

func TestCheckProviderBinanceCombinesPermissionsAcrossActiveKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-MBX-APIKEY") {
		case "trade-key":
			_ = json.NewEncoder(w).Encode(map[string]any{"enableReading": true, "enableSpotAndMarginTrading": true, "enableWithdrawals": false, "enableFutures": false})
		case "transfer-key":
			_ = json.NewEncoder(w).Encode(map[string]any{"enableReading": false, "enableSpotAndMarginTrading": false, "enableWithdrawals": true, "enableFutures": true})
		default:
			t.Fatalf("unexpected API key %q", r.Header.Get("X-MBX-APIKEY"))
		}
	}))
	t.Cleanup(srv.Close)

	repo := &fakeRepo{accountProvider: "Binance", keys: []db.APIKey{
		{ID: 1, Name: "trade", APIKey: "trade-key", APISecret: "secret", IsActive: true},
		{ID: 2, Name: "transfer", APIKey: "transfer-key", APISecret: "secret", IsActive: true},
	}}
	result, err := accounthealth.NewChecker(repo, cexaccount.Config{BinanceBaseURL: srv.URL}).CheckProvider(context.Background(), "binance")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || len(result.Keys) != 2 {
		t.Fatalf("result = %+v, want combined healthy result", result)
	}
	if len(repo.permissionUpdates) != 2 {
		t.Fatalf("permission updates = %d, want 2", len(repo.permissionUpdates))
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
	checker := accounthealth.NewChecker(repo, cexaccount.Config{RobosatsBaseURL: srv.URL})
	result, err := checker.CheckProvider(context.Background(), "robosats")
	if err != nil {
		t.Fatalf("CheckProvider: %v", err)
	}
	if result.Status != "unhealthy" || result.Healthy {
		t.Fatalf("result = %+v, want unhealthy false", result)
	}
}
