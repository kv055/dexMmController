package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubEnqueuer struct{ enqueued []uint }

func (s *stubEnqueuer) Enqueue(txID uint) bool { s.enqueued = append(s.enqueued, txID); return true }

func TestDexReleaseWebhookNotConfigured(t *testing.T) {
	s := NewServer(":0", nil) // no dexRelease/token wired
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/dex-release", strings.NewReader(`{"transaction_id":1}`))
	s.handleDexReleaseWebhook(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestDexReleaseWebhookRejectsBadToken(t *testing.T) {
	s := NewServer(":0", nil).WithDexRelease(&stubEnqueuer{}, "s3cr3t")
	cases := []struct{ name, auth string }{
		{"missing", ""},
		{"wrong scheme", "Token s3cr3t"},
		{"wrong value", "Bearer nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/webhooks/dex-release", strings.NewReader(`{"transaction_id":1}`))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			s.handleDexReleaseWebhook(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestAuthorizeDexReleaseConstantTimeMatch(t *testing.T) {
	s := NewServer(":0", nil).WithDexRelease(&stubEnqueuer{}, "good-token")
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	if !s.authorizeDexRelease(req) {
		t.Fatal("valid token rejected")
	}
	req.Header.Set("Authorization", "Bearer good-token-extra")
	if s.authorizeDexRelease(req) {
		t.Fatal("invalid token accepted")
	}
}
