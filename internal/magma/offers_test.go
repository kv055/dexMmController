package magma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOffersClientListOffers(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer key-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotQuery = body.Query
		_, _ = w.Write([]byte(`{
			"data": {
				"getOffers": {
					"list": [
						{"id":"b","side":"SELL","status":"ENABLED","base_fee":500,"fee_rate":3000,"min_size":"1000000","max_size":"2000000","total_size":"4000000","min_block_length":4320,"seller_score":"5.5","account":"02bbb"},
						{"id":"a","side":"SELL","status":"ENABLED","base_fee":900,"fee_rate":1500,"min_size":"300000","max_size":"8000000","total_size":"8000000","min_block_length":12960,"seller_score":"9.9","account":"02aaa"},
						{"id":"c","side":"BUY","status":"ENABLED","base_fee":0,"fee_rate":10,"min_size":"1","max_size":"2","total_size":"2","min_block_length":144,"seller_score":"1.0","account":"02ccc"},
						{"id":"d","side":"SELL","status":"DISABLED","base_fee":0,"fee_rate":20,"min_size":"1","max_size":"2","total_size":"2","min_block_length":144,"seller_score":"1.0","account":"02ddd"}
					]
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewOffersClient(server.URL+"/graphql", "key-1")
	offers, err := client.ListOffers(context.Background())
	if err != nil {
		t.Fatalf("ListOffers: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("expected 2 enabled sell offers, got %d", len(offers))
	}
	if offers[0].ID != "a" || offers[1].ID != "b" {
		t.Fatalf("offers not sorted by cheapest fee: %s before %s", offers[0].ID, offers[1].ID)
	}
	if offers[0].MinSizeSat != 300000 || offers[0].MaxSizeSat != 8000000 || offers[0].TotalSizeSat != 8000000 {
		t.Fatalf("sat parsing failed: %+v", offers[0])
	}
	if offers[0].SellerPubkey != "02aaa" || offers[0].MinBlockLength != 12960 {
		t.Fatalf("offer fields not mapped: %+v", offers[0])
	}
	if gotQuery == "" {
		t.Fatal("GraphQL query was not sent")
	}

	var badClient *OffersClient
	if _, err := badClient.ListOffers(context.Background()); err == nil {
		t.Fatal("expected error for nil offers client")
	}
}
