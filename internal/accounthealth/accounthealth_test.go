package accounthealth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dexMmController/internal/db"
)

type fakeRepo struct {
	accountProvider string
	keys            []db.APIKey
	updatedProvider string
	updatedHealthy  bool
	checkedAt       time.Time
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
	if gotAuth != "Token robot-token" {
		t.Fatalf("Authorization = %q, want Token robot-token", gotAuth)
	}
	if repo.updatedProvider != "Robosats" || !repo.updatedHealthy {
		t.Fatalf("updated provider=%q healthy=%v, want Robosats true", repo.updatedProvider, repo.updatedHealthy)
	}
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
