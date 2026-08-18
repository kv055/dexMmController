package magma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetInfoCreateOrderGetOrder(t *testing.T) {
	var createBody CreateOrderRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/lsp/v1/recommended/get_info":
			_ = json.NewEncoder(w).Encode(Info{
				MinRequiredChannelConfirmations: 0,
				MinFundingConfirmsWithinBlocks:  6,
				MaxChannelExpiryBlocks:            12960,
				MinInitialLSPBalanceSat:           "500000",
				MaxInitialLSPBalanceSat:           "100000000",
				URIs:                              []string{"03abc@lsp.example.com:9735"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/lsp/v1/recommended/create_order":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create_order: %v", err)
			}
			_ = json.NewEncoder(w).Encode(Order{
				OrderID:      "order-1",
				OrderState:   "CREATED",
				LSPBalanceSat: createBody.LSPBalanceSat,
				Payment: Payment{Bolt11: PaymentBolt11{
					State:         "EXPECT_PAYMENT",
					FeeTotalSat:   "12500",
					OrderTotalSat: "12500",
					Invoice:       "lnbc125u1p...",
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/lsp/v1/recommended/get_order":
			if r.URL.Query().Get("order_id") != "order-1" {
				http.Error(w, `{"statusCode":404,"message":"Order not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(Order{
				OrderID:    "order-1",
				OrderState: "COMPLETED",
				Payment:    Payment{Bolt11: PaymentBolt11{State: "PAID"}},
				Channel:    &Channel{FundingOutpoint: "txid:0"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL + "/api/lsp/v1")
	info, err := client.GetInfo(context.Background(), PoolRecommended)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if info.MinInitialLSPBalanceSat != "500000" {
		t.Fatalf("min LSP balance = %q", info.MinInitialLSPBalanceSat)
	}

	order, err := client.CreateOrder(context.Background(), PoolRecommended, CreateOrderRequest{
		LSPBalanceSat:                "2000000",
		ClientBalanceSat:             "0",
		RequiredChannelConfirmations: 6,
		FundingConfirmsWithinBlocks:  6,
		ChannelExpiryBlocks:          4320,
		Token:                        "",
		AnnounceChannel:              false,
		PublicKey:                    "03local",
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.Payment.Bolt11.Invoice == "" || createBody.PublicKey != "03local" {
		t.Fatalf("unexpected create order: %+v body=%+v", order, createBody)
	}

	polled, err := client.GetOrder(context.Background(), PoolRecommended, "order-1")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if polled.Channel == nil || polled.Channel.FundingOutpoint != "txid:0" {
		t.Fatalf("unexpected polled order: %+v", polled)
	}
}

func TestParseURI(t *testing.T) {
	pubkey, host, err := ParseURI("03abc@1.2.3.4:9735")
	if err != nil || pubkey != "03abc" || host != "1.2.3.4:9735" {
		t.Fatalf("ParseURI = (%q,%q,%v)", pubkey, host, err)
	}
}

func TestPoolValidate(t *testing.T) {
	if err := Pool("invalid").Validate(); err == nil {
		t.Fatal("expected invalid pool error")
	}
}
