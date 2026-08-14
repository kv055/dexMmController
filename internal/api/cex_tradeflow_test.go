package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/pretrade"
	"dexMmController/internal/tradeflow"
)

type fakeTradeFlowRunner struct {
	gotOrder        tradeflow.Order
	result          *tradeflow.Result
	err             error
	reconcileResult *db.Transaction
	reconcileErr    error
}

func (f *fakeTradeFlowRunner) ReconcilePoolRefill(_ context.Context, _ uint) (*db.Transaction, error) {
	return f.reconcileResult, f.reconcileErr
}

func (f *fakeTradeFlowRunner) Run(_ context.Context, order tradeflow.Order) (*tradeflow.Result, error) {
	f.gotOrder = order
	return f.result, f.err
}

type fakePreTradeChecker struct {
	params pretrade.Params
}

func (f *fakePreTradeChecker) Run(_ context.Context, params pretrade.Params) pretrade.Report {
	f.params = params
	return pretrade.Report{OK: true}
}

func (f *fakePreTradeChecker) RunAll(_ context.Context, _ pretrade.Params) error { return nil }

func TestHandleCexPreTradeCheckRequiresUnifiedAPIRouteSnapshot(t *testing.T) {
	checker := &fakePreTradeChecker{}
	s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, cexaccount.Config{}).WithPreTradeChecker(checker))
	req := httptest.NewRequest(http.MethodPost, "/api/cex/pre-trade-check", bytes.NewBufferString(`{"provider":"binance","fiat_currency":"EUR","required_capital":100}`))
	rec := httptest.NewRecorder()

	s.handleCexPreTradeCheck(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "resolved_route from unified API is required" {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestHandleCexPreTradeCheckAcceptsUnifiedAPISettlements(t *testing.T) {
	for _, settlement := range []string{"onchain_direct", "onchain_hub", "onchain_swap"} {
		t.Run(settlement, func(t *testing.T) {
			checker := &fakePreTradeChecker{}
			s := (&Server{}).WithCexAccount(cexaccount.NewService(nil, cexaccount.Config{}).WithPreTradeChecker(checker))
			payload := map[string]any{
				"provider": "binance", "fiat_currency": "EUR", "required_capital": 100,
				"settlement": settlement,
			}
			if settlement == "onchain_swap" {
				payload["required_btc_liquidity"] = 0.01
				payload["routing_fee_allowance_sat"] = 50
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/cex/pre-trade-check", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			s.handleCexPreTradeCheck(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if checker.params.Settlement != settlement {
				t.Fatalf("settlement = %q, want %q", checker.params.Settlement, settlement)
			}
		})
	}
}

func TestHandleCexPreTradeCheckUsesConfiguredLightningSource(t *testing.T) {
	checker := &fakePreTradeChecker{}
	repo := &fakeTransactionalLiquiditySettingsRepo{row: db.TransactionalLiquiditySettings{
		LightningLiquiditySource: db.TransactionalLiquidityLSPChannel,
	}}
	s := (&Server{}).
		WithCexAccount(cexaccount.NewService(nil, cexaccount.Config{}).WithPreTradeChecker(checker)).
		WithTransactionalLiquiditySettings(repo)
	route := authoritativeTestRoute(cexaccount.TradeModeHedgefullSingleCollateral, "onchain_swap")
	body, err := json.Marshal(map[string]any{"resolved_route": route})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cex/pre-trade-check", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleCexPreTradeCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if checker.params.LightningLiquiditySource != pretrade.LightningLiquidityLSPChannel {
		t.Fatalf("lightning source = %q", checker.params.LightningLiquiditySource)
	}
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
			Withdrawal: &tradeflow.WithdrawalResult{
				WithdrawalID:  "wd-1",
				WalletAddress: "bc1qtest",
				AmountBTC:     0.01,
			},
		},
	}
	s := (&Server{}).WithTradeFlow(runner)
	payload, _ := json.Marshal(tradeflowRequest{
		Provider:         "binance",
		MarketSide:       "bid",
		BTCAmount:        0.01,
		FiatCurrency:     "EUR",
		RequiredCapital:  1000,
		HedgeVenue:       "binance_futures",
		HedgeSide:        "short",
		HedgeNotionalBTC: 0.01,
		Leverage:         10,
		ResolvedRoute:    &tradeflow.ResolvedRoute{ConfigVersion: 1, TradeMode: cexaccount.TradeModeHedgefullSingleCollateral, MarketSide: "bid", Provider: "binance", HedgeVenue: "binance_futures", HedgeSide: "short", HedgeNotional: 0.01, Leverage: 10, Settlement: "onchain_hub", BTCAmount: 0.01, FiatCurrency: "EUR", FiatAmount: 500, RequiredCapital: 1000},
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

func TestHandleCexTradeflowAcceptsHedgelessWithoutHedgeFields(t *testing.T) {
	runner := &fakeTradeFlowRunner{result: &tradeflow.Result{TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral}}
	s := (&Server{}).WithTradeFlow(runner)
	payload, _ := json.Marshal(tradeflowRequest{
		Provider: "binance", MarketSide: "bid", BTCAmount: 0.01, FiatCurrency: "EUR",
		RequiredCapital: 500, RequiredBTCLiquidity: 0.0115, SecurityDepositBTC: 0.0015,
		OfferID: "offer-1", Dex: "hodlhodl", TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral,
		ResolvedRoute: &tradeflow.ResolvedRoute{ConfigVersion: 1, TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral, MarketSide: "bid", Provider: "binance", Settlement: "onchain_hub", NeedsHub: true, BTCAmount: 0.01, FiatCurrency: "EUR", FiatAmount: 500, RequiredCapital: 500, RequiredBTCLiquidity: 0.0115, SecurityDepositBTC: 0.0015},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/cex/tradeflow", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.handleCexTradeflow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if runner.gotOrder.RequiredBTCLiquidity != 0.0115 || runner.gotOrder.HedgeVenue != "" {
		t.Fatalf("order = %+v", runner.gotOrder)
	}
}

func TestHandleCexTradeflowRejectsHedgelessWithoutLiquidity(t *testing.T) {
	s := (&Server{}).WithTradeFlow(&fakeTradeFlowRunner{})
	body := []byte(`{"provider":"binance","market_side":"bid","btc_amount":0.01,"required_capital":500,"trade_mode":"hedgeless_double_collateral"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cex/tradeflow", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleCexTradeflow(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlePoolRefillReconcile(t *testing.T) {
	runner := &fakeTradeFlowRunner{reconcileResult: &db.Transaction{ID: 42, BTCPoolRefillStatus: "restored"}}
	s := (&Server{}).WithTradeFlow(runner)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/42/pool-refill/reconcile", nil)
	req.SetPathValue("id", "42")
	rec := httptest.NewRecorder()
	s.handlePoolRefillReconcile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
