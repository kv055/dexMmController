package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestStartupProbesReflectReadiness(t *testing.T) {
	state := NewStartupState()
	server := NewServer(":0", nil).WithStartupState(state)

	live := httptest.NewRecorder()
	server.handleLivez(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("livez status = %d, want 200", live.Code)
	}

	notReady := httptest.NewRecorder()
	server.handleReadyz(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("initial readyz status = %d, want 503", notReady.Code)
	}
	legacyHealth := httptest.NewRecorder()
	server.handleHealth(legacyHealth, httptest.NewRequest(http.MethodGet, "/health", nil))
	if legacyHealth.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-database health status = %d, want 503", legacyHealth.Code)
	}

	state.SetDatabaseReady(true, "lnd_bootstrap", nil)
	stillPending := httptest.NewRecorder()
	server.handleReadyz(stillPending, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if stillPending.Code != http.StatusServiceUnavailable {
		t.Fatalf("database-only readyz status = %d, want 503", stillPending.Code)
	}

	state.SetLNDReady(true, "ready", nil)
	ready := httptest.NewRecorder()
	server.handleReadyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("fully-ready readyz status = %d, want 200", ready.Code)
	}
}

func TestStartupGateBlocksRoutesUntilDatabaseReady(t *testing.T) {
	state := NewStartupState()
	server := NewServer(":0", nil).WithStartupState(state)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, path := range []string{"/api/transactions", "/api/lnd/wallet/state"} {
		rec := httptest.NewRecorder()
		server.startupGate(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, rec.Code)
		}
	}
	state.SetDatabaseReady(true, "lnd_bootstrap", nil)
	rec := httptest.NewRecorder()
	server.startupGate(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lnd/wallet/state", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("LND route status after database readiness = %d, want 204", rec.Code)
	}
}

func TestStartupStateConcurrentUpdates(t *testing.T) {
	state := NewStartupState()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state.SetDatabaseReady(i%2 == 0, "database", errors.New("temporary database failure"))
			state.SetLNDReady(i%3 == 0, "lnd", nil)
			_ = state.Snapshot()
		}(i)
	}
	wg.Wait()
	if state.Snapshot().Phase == "" {
		t.Fatal("phase must always be populated")
	}
}
