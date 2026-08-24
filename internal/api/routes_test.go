package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeneratedAndRawRoutesAreRegistered(t *testing.T) {
	mux := http.NewServeMux()
	NewServer(":0", nil).registerRoutes(mux)

	tests := []struct {
		method  string
		path    string
		pattern string
	}{
		{http.MethodGet, "/livez", "GET /livez"},
		{http.MethodPost, "/api/webhooks/dex-release", "POST /api/webhooks/dex-release"},
		{http.MethodGet, "/api/transactions/42", "GET /api/transactions/{id}"},
		{http.MethodPost, "/api/cex/pre-trade-check", "POST /api/cex/pre-trade-check"},
		{http.MethodGet, "/ws/cex/hedge-stream", "GET /ws/cex/hedge-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			_, got := mux.Handler(req)
			if got != tt.pattern {
				t.Fatalf("matched pattern %q, want %q", got, tt.pattern)
			}
		})
	}

	t.Run("strict router rejects malformed JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/cex/pre-trade-check", strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
		}
		if !strings.Contains(recorder.Body.String(), "can't decode JSON body") {
			t.Fatalf("response does not come from strict decoder: %q", recorder.Body.String())
		}
	})

	t.Run("contentless webhook error satisfies contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/telegram-webhook", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
		}
		if recorder.Body.Len() != 0 {
			t.Fatalf("body = %q, want empty", recorder.Body.String())
		}
	})
}
