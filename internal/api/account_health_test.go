package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/db"
	"dexMmController/internal/lndhealth"
)

type fakeAccountHealthChecker struct {
	calledProvider string
}

func (f *fakeAccountHealthChecker) CheckProvider(_ context.Context, provider string) (accounthealth.Result, error) {
	f.calledProvider = provider
	return accounthealth.Result{
		Provider:  "Kraken",
		Status:    "healthy",
		Healthy:   true,
		Message:   "ok",
		CheckedAt: time.Date(2026, 4, 23, 1, 2, 3, 0, time.UTC),
	}, nil
}

func TestHandleAccountHealthCheckRequiresProvider(t *testing.T) {
	s := (&Server{}).WithAccountHealth(&fakeAccountHealthChecker{})
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/health_check", nil)
	rec := httptest.NewRecorder()
	s.handleAccountHealthCheck(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAccountHealthCheckRejectsUnsupportedProvider(t *testing.T) {
	s := (&Server{}).WithAccountHealth(&fakeAccountHealthChecker{})
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/health_check?provider=Bisq", nil)
	rec := httptest.NewRecorder()
	s.handleAccountHealthCheck(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type fakeLNDHealthChecker struct {
	called bool
}

func (f *fakeLNDHealthChecker) Check(context.Context) (lndhealth.Result, error) {
	f.called = true
	return lndhealth.Result{
		Provider:  db.LNDAccountProvider,
		Status:    "healthy",
		Healthy:   true,
		Message:   "ok",
		CheckedAt: time.Date(2026, 4, 23, 1, 2, 3, 0, time.UTC),
	}, nil
}

func TestHandleAccountHealthCheckRoutesLNDWallet(t *testing.T) {
	lndChecker := &fakeLNDHealthChecker{}
	s := (&Server{}).WithLNDHealth(lndChecker)
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/health_check?provider=LND%20wallet", nil)
	rec := httptest.NewRecorder()
	s.handleAccountHealthCheck(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !lndChecker.called {
		t.Fatal("expected LND health checker to be called")
	}
}

func TestHandleAccountHealthCheckRunsRequestedProviderOnly(t *testing.T) {
	checker := &fakeAccountHealthChecker{}
	s := (&Server{}).WithAccountHealth(checker)
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/health_check?provider=kraken", nil)
	rec := httptest.NewRecorder()
	s.handleAccountHealthCheck(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if checker.calledProvider != "kraken" {
		t.Fatalf("called provider = %q, want raw query provider kraken", checker.calledProvider)
	}
}
