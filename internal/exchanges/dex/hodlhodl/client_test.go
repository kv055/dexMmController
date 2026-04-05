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

func TestSearchOffers(t *testing.T) {
	_, c := newTestServer(t, map[string]any{
		"/api/v1/offers": map[string]any{
			"status": "success",
			"offers": []map[string]any{
				{"id": "o1", "side": "buy", "price": "95000", "currency_code": "EUR", "status": "active"},
				{"id": "o2", "side": "buy", "price": "96000", "currency_code": "EUR", "status": "active"},
			},
		},
	})

	offers, err := c.SearchOffers(context.Background(), OfferSearchFilters{
		Side:         "buy",
		CurrencyCode: "EUR",
	})
	if err != nil {
		t.Fatalf("SearchOffers: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("got %d offers, want 2", len(offers))
	}
	if offers[0].Price != "95000" {
		t.Errorf("first offer price=%q, want 95000", offers[0].Price)
	}
}

func TestCreateContract(t *testing.T) {
	_, c := newTestServer(t, map[string]any{
		"/api/v1/contracts": map[string]any{
			"status": "success",
			"contract": map[string]any{
				"id":     "ct1",
				"status": "pending",
			},
		},
	})

	contract, err := c.CreateContract(context.Background(), CreateContractParams{
		OfferID:                     "o1",
		OfferVersion:                "v1",
		PaymentMethodInstructionID:  "pmi1",
		PaymentMethodInstructionVer: "pmv1",
		Value:                       "1000",
	})
	if err != nil {
		t.Fatalf("CreateContract: %v", err)
	}
	if contract.ID != "ct1" {
		t.Errorf("got ID=%q, want ct1", contract.ID)
	}
	if contract.Status != ContractPending {
		t.Errorf("got Status=%q, want pending", contract.Status)
	}
}

func TestTakeOffer(t *testing.T) {
	mux := http.NewServeMux()
	// Offer detail endpoint.
	offerBody, _ := json.Marshal(map[string]any{
		"status": "success",
		"offer":  map[string]any{"id": "o1", "version": "v3", "status": "active"},
	})
	mux.HandleFunc("/api/v1/offers/o1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(offerBody)
	})
	// Payment method instructions endpoint.
	pmiBody, _ := json.Marshal(map[string]any{
		"status": "success",
		"payment_method_instructions": []map[string]any{
			{"id": "pmi-42", "version": "pv1", "name": "My Bank Account"},
		},
	})
	mux.HandleFunc("/api/v1/payment_method_instructions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(pmiBody)
	})
	// Contracts endpoint — capture what was posted.
	var gotOfferID, gotPMI, gotValue string
	contractBody, _ := json.Marshal(map[string]any{
		"status": "success",
		"contract": map[string]any{
			"id":     "ct1",
			"status": "pending",
			"escrow": map[string]any{"address": "2N7e9vRXjiy1UyUcpZjjKbhQP7iEC2QB31t"},
		},
	})
	mux.HandleFunc("/api/v1/contracts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req map[string]map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			gotOfferID, _ = req["contract"]["offer_id"].(string)
			gotPMI, _ = req["contract"]["payment_method_instruction_id"].(string)
			gotValue, _ = req["contract"]["value"].(string)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(contractBody)
	})
	// GetContract poll endpoint (escrow address already present so no polling needed).
	mux.HandleFunc("/api/v1/contracts/ct1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(contractBody)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-key")

	escrowAddr, contract, err := c.TakeOffer(context.Background(), "o1", "1000")
	if err != nil {
		t.Fatalf("TakeOffer: %v", err)
	}
	if escrowAddr != "2N7e9vRXjiy1UyUcpZjjKbhQP7iEC2QB31t" {
		t.Errorf("got escrow address=%q, want 2N7e9vRXjiy1UyUcpZjjKbhQP7iEC2QB31t", escrowAddr)
	}
	if contract.ID != "ct1" {
		t.Errorf("got contract ID=%q, want ct1", contract.ID)
	}
	if gotOfferID != "o1" {
		t.Errorf("posted offer_id=%q, want o1", gotOfferID)
	}
	if gotPMI != "pmi-42" {
		t.Errorf("posted payment_method_instruction_id=%q, want pmi-42", gotPMI)
	}
	if gotValue != "1000" {
		t.Errorf("posted value=%q, want 1000", gotValue)
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
