package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalSecretMiddleware(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("rejects missing and wrong secrets, accepts the right one", func(t *testing.T) {
		s := (&Server{}).WithInternalSecret("s3cret")
		h := s.internalSecretMiddleware(okHandler)

		cases := []struct {
			name   string
			header string
			want   int
		}{
			{"missing", "", http.StatusUnauthorized},
			{"wrong", "nope", http.StatusUnauthorized},
			{"correct", "s3cret", http.StatusOK},
		}
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
			if tc.header != "" {
				req.Header.Set("X-Internal-Secret", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
			}
		}
	})

	t.Run("exempt routes pass without a secret", func(t *testing.T) {
		s := (&Server{}).WithInternalSecret("s3cret")
		h := s.internalSecretMiddleware(okHandler)
		for _, path := range []string{"/health", "/metrics", "/api/webhooks/dex-release", "/telegram-webhook"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("exempt %s: got %d, want 200", path, rec.Code)
			}
		}
	})

	t.Run("empty secret disables the gate", func(t *testing.T) {
		s := &Server{}
		h := s.internalSecretMiddleware(okHandler)
		req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("empty secret: got %d, want 200", rec.Code)
		}
	})
}
