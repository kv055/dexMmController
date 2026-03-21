package hodlhodl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer starts an httptest server that routes HodlHodl-style paths
// and returns the canned responses registered in the handlers map.
func newTestServer(t *testing.T, handlers map[string]any) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, resp := range handlers {
		body, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal handler response: %v", err)
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-api-key")
	return srv, c
}

func TestGetOffer(t *testing.T) {
	_, c := newTestServer(t, map[string]any{
		"/api/v1/offers/offer123": map[string]any{
			"status": "success",
			"offer": map[string]any{
				"id":     "offer123",
				"status": "active",
				"your_role": "seller",
				"title":  "Sell BTC for EUR",
			},
		},
	})

	offer, err := c.GetOffer(context.Background(), "offer123")
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}
	if offer.ID != "offer123" {
		t.Errorf("got ID=%q, want offer123", offer.ID)
	}
	if offer.Status != "active" {
		t.Errorf("got Status=%q, want active", offer.Status)
	}
}

func TestIsOfferLive(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantLive   bool
	}{
		{"active offer", "active", true},
		{"paused offer", "paused", false},
		{"removed offer", "removed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newTestServer(t, map[string]any{
				"/api/v1/offers/o1": map[string]any{
					"status": "success",
					"offer":  map[string]any{"id": "o1", "status": tt.status},
				},
			})
			live, err := c.IsOfferLive(context.Background(), "o1")
			if err != nil {
				t.Fatalf("IsOfferLive: %v", err)
			}
			if live != tt.wantLive {
				t.Errorf("got live=%v, want %v", live, tt.wantLive)
			}
		})
	}
}

func TestGetContract(t *testing.T) {
	_, c := newTestServer(t, map[string]any{
		"/api/v1/contracts/c1": map[string]any{
			"status": "success",
			"contract": map[string]any{
				"id":     "c1",
				"status": "in_progress",
				"your_role": "seller",
				"volume": "0.05", "value": "3000",
			},
		},
	})

	contract, err := c.GetContract(context.Background(), "c1")
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	if contract.ID != "c1" {
		t.Errorf("got ID=%q, want c1", contract.ID)
	}
	if contract.Status != ContractInProgress {
		t.Errorf("got Status=%q, want %q", contract.Status, ContractInProgress)
	}
}

func TestIsContractCompleted(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		wantDone bool
	}{
		{"completed", "completed", true},
		{"in_progress", "in_progress", false},
		{"paid", "paid", false},
		{"canceled", "canceled", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newTestServer(t, map[string]any{
				"/api/v1/contracts/c1": map[string]any{
					"status":   "success",
					"contract": map[string]any{"id": "c1", "status": tt.status},
				},
			})
			done, err := c.IsContractCompleted(context.Background(), "c1")
			if err != nil {
				t.Fatalf("IsContractCompleted: %v", err)
			}
			if done != tt.wantDone {
				t.Errorf("got %v, want %v", done, tt.wantDone)
			}
		})
	}
}

func TestContractIsPaid(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		wantPaid bool
	}{
		{"paid", "paid", true},
		{"completed", "completed", true},
		{"in_progress", "in_progress", false},
		{"pending", "pending", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newTestServer(t, map[string]any{
				"/api/v1/contracts/c1": map[string]any{
					"status":   "success",
					"contract": map[string]any{"id": "c1", "status": tt.status},
				},
			})
			paid, err := c.ContractIsPaid(context.Background(), "c1")
			if err != nil {
				t.Fatalf("ContractIsPaid: %v", err)
			}
			if paid != tt.wantPaid {
				t.Errorf("got %v, want %v", paid, tt.wantPaid)
			}
		})
	}
}

func TestContractHasCounterparty(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantHas bool
	}{
		{"pending — no counterparty", "pending", false},
		{"in_progress — has counterparty", "in_progress", true},
		{"paid — has counterparty", "paid", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newTestServer(t, map[string]any{
				"/api/v1/contracts/c1": map[string]any{
					"status":   "success",
					"contract": map[string]any{"id": "c1", "status": tt.status},
				},
			})
			has, err := c.ContractHasCounterparty(context.Background(), "c1")
			if err != nil {
				t.Fatalf("ContractHasCounterparty: %v", err)
			}
			if has != tt.wantHas {
				t.Errorf("got %v, want %v", has, tt.wantHas)
			}
		})
	}
}

func TestAPIError(t *testing.T) {
	_, c := newTestServer(t, map[string]any{
		"/api/v1/offers/bad": map[string]any{
			"status":     "error",
			"error_code": "not_found",
		},
	})

	_, err := c.GetOffer(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestAuthHeaderSent(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"offer":  map[string]any{"id": "x", "status": "active"},
		})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "my-secret-key")
	_, _ = c.GetOffer(context.Background(), "x")

	if gotAuth != "Bearer my-secret-key" {
		t.Errorf("got Authorization=%q, want 'Bearer my-secret-key'", gotAuth)
	}
}
