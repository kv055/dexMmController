package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeState(t *testing.T, body []byte) lndWalletStateResponse {
	t.Helper()
	var resp lndWalletStateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode state response: %v (body=%s)", err, body)
	}
	return resp
}

func TestHandleLNDWalletStateCanCreateWhenNonExisting(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		StateProbe: func(context.Context) (string, error) { return "NON_EXISTING", nil },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet/state", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	state := decodeState(t, rec.Body.Bytes())
	if !state.LNDReachable || !state.CanCreate || state.WalletExists || state.ClientReady {
		t.Fatalf("unexpected state: %+v", state)
	}
	if state.LNDState != "NON_EXISTING" {
		t.Fatalf("lnd_state = %q, want NON_EXISTING", state.LNDState)
	}
}

func TestHandleLNDWalletStateWalletExistsWhenLocked(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		StateProbe: func(context.Context) (string, error) { return "LOCKED", nil },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet/state", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletState(rec, req)

	state := decodeState(t, rec.Body.Bytes())
	if !state.LNDReachable || state.CanCreate || !state.WalletExists {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestHandleLNDWalletStateIncludesLastInitializationError(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		StateProbe:    func(context.Context) (string, error) { return "LOCKED", nil },
		LastInitError: func() string { return "unlocking wallet: invalid passphrase" },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet/state", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletState(rec, req)

	state := decodeState(t, rec.Body.Bytes())
	if state.LastInitError != "unlocking wallet: invalid passphrase" {
		t.Fatalf("last_init_error = %q", state.LastInitError)
	}
}

func TestHandleLNDWalletStateUnreachableWhenProbeFails(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		StateProbe: func(context.Context) (string, error) { return "", errors.New("dial tcp: refused") },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet/state", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletState(rec, req)

	state := decodeState(t, rec.Body.Bytes())
	if state.LNDReachable || state.CanCreate || state.WalletExists {
		t.Fatalf("expected unreachable state, got: %+v", state)
	}
}

func TestHandleLNDWalletInitNotConfigured(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletInit(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleLNDWalletInitMissingPasswordIs400(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		Init: func(context.Context) (LNDInitResult, error) {
			return LNDInitResult{}, ErrLNDWalletPasswordMissing
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletInit(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleLNDWalletInitInvalidPassphraseIs409(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		Init: func(context.Context) (LNDInitResult, error) {
			return LNDInitResult{}, errors.New("unlocking wallet: invalid passphrase for master public key")
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletInit(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestHandleLNDWalletInitSuccess(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		Init: func(context.Context) (LNDInitResult, error) {
			return LNDInitResult{PrimaryAddress: "bc1qexample"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletInit(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp lndWalletInitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode init response: %v", err)
	}
	if resp.PrimaryAddress != "bc1qexample" {
		t.Fatalf("primary_address = %q, want bc1qexample", resp.PrimaryAddress)
	}
}

func TestRegisteredLNDWalletInitPassesCreatedResponseValidation(t *testing.T) {
	s := (&Server{}).WithLNDManagement(LNDManagement{
		Init: func(context.Context) (LNDInitResult, error) {
			return LNDInitResult{PrimaryAddress: "bc1qvalidated"}, nil
		},
	})
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("validated route status = %d, want %d (body=%s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("validated route content type = %q, want application/json", got)
	}
}

func TestHandleLNDWalletInitIdempotentWhenClientWired(t *testing.T) {
	initCalls := 0
	s := (&Server{}).
		WithLNDWallet(&fakeLNDWalletClient{}).
		WithLNDManagement(LNDManagement{
			Init: func(context.Context) (LNDInitResult, error) {
				initCalls++
				return LNDInitResult{}, nil
			},
		})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/wallet/init", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletInit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if initCalls != 0 {
		t.Fatalf("Init was called %d times; expected 0 (already initialized)", initCalls)
	}
	var resp lndWalletInitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode init response: %v", err)
	}
	if !resp.AlreadyInitialized {
		t.Fatalf("expected already_initialized=true, got %+v", resp)
	}
}
