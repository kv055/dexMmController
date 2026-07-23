package cexaccount

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFiatControllerCheckerRequiresHealthyControllerAndFreshHeartbeat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/notification-health":
			if r.Header.Get("X-Webhook-Secret") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("payment_method") != "Wise" {
				t.Errorf("payment_method = %q", r.URL.Query().Get("payment_method"))
			}
			_, _ = w.Write([]byte(`{"selected_channels":["push","email"],"channels":{"push":{"healthy":true},"email":{"healthy":true}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := NewHTTPFiatControllerChecker(server.URL, "secret", server.Client()).Check(context.Background(), "Wise"); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestHTTPFiatControllerCheckerRejectsUnhealthySelectedChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(`{"selected_channels":["email"],"channels":{"email":{"healthy":false}}}`))
	}))
	defer server.Close()

	err := NewHTTPFiatControllerChecker(server.URL, "", server.Client()).Check(context.Background(), "PayPal")
	if err == nil || !strings.Contains(err.Error(), "email notification channel is unhealthy") {
		t.Fatalf("Check error = %v, want unhealthy email failure", err)
	}
}
