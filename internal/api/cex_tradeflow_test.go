package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/tradeflow"
)

type fakeTradeFlowRunner struct {
	gotOrder tradeflow.Order
	result   *tradeflow.Result
	err      error
}

func (f *fakeTradeFlowRunner) Run(_ context.Context, order tradeflow.Order) (*tradeflow.Result, error) {
	f.gotOrder = order
	return f.result, f.err
}

func TestHandleCexTradeflowRequiresProvider(t *testing.T) {
	runner := &fakeTradeFlowRunner{}
	s := (&Server{}).WithTradeFlow(runner)
	body := []byte(`{"btc_amount":0.01,"required_capital":100,"hedge_venue":"binance_futures","hedge_notional_btc":0.01}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cex/tradeflow", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleCexTradeflow(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCexTradeflowSuccess(t *testing.T) {
	runner := &fakeTradeFlowRunner{
		result: &tradeflow.Result{
			SpotBuy: &cexaccount.SpotOrderResult{
				Provider: "binance",
				OrderID:  "spot-1",
				Status:   "filled",
			},
			Hedge: &cexaccount.HedgeOpenResult{
				Venue:   "binance_futures",
				OrderID: "hedge-1",
				Status:  "filled",
			},
			Withdrawal: tradeflow.WithdrawalResult{
				WithdrawalID:  "wd-1",
				WalletAddress: "bc1qtest",
				AmountBTC:     0.01,
			},
		},
	}
	s := (&Server{}).WithTradeFlow(runner)
	payload, _ := json.Marshal(tradeflowRequest{
		Provider:           "binance",
		MarketSide:         "bid",
		BTCAmount:          0.01,
		FiatCurrency:       "EUR",
		RequiredCapital: 1000,
		HedgeVenue:         "binance_futures",
		HedgeSide:          "short",
		HedgeNotionalBTC:   0.01,
		Leverage:           10,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/cex/tradeflow", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleCexTradeflow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp tradeflow.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SpotBuy == nil || resp.SpotBuy.OrderID != "spot-1" {
		t.Fatalf("spot_buy: %+v", resp.SpotBuy)
	}
	if resp.Hedge == nil || resp.Hedge.OrderID != "hedge-1" {
		t.Fatalf("hedge: %+v", resp.Hedge)
	}
	if resp.Withdrawal.WithdrawalID != "wd-1" {
		t.Fatalf("withdrawal_id: %q", resp.Withdrawal.WithdrawalID)
	}
	if runner.gotOrder.Provider != "binance" || runner.gotOrder.MarketSide != "bid" {
		t.Fatalf("order passed to runner: %+v", runner.gotOrder)
	}
}

func TestHandleCexTradeflowNotConfigured(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/cex/tradeflow", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	s.handleCexTradeflow(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
