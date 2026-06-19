package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/cexaccount"
)

func TestHandleCexWithdrawalTrackNotConfigured(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/cex/withdrawal-track?provider=binance&withdrawal_id=wd-1", nil)
	rec := httptest.NewRecorder()
	s.handleCexWithdrawalTrack(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleCexWithdrawalTrackRequiresProvider(t *testing.T) {
	s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, nil, accounthealthConfig()))
	req := httptest.NewRequest(http.MethodGet, "/api/cex/withdrawal-track?withdrawal_id=wd-1", nil)
	rec := httptest.NewRecorder()
	s.handleCexWithdrawalTrack(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCexWithdrawalTrackRequiresWithdrawalID(t *testing.T) {
	s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, nil, accounthealthConfig()))
	req := httptest.NewRequest(http.MethodGet, "/api/cex/withdrawal-track?provider=binance", nil)
	rec := httptest.NewRecorder()
	s.handleCexWithdrawalTrack(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func accounthealthConfig() accounthealth.Config {
	return accounthealth.Config{}
}