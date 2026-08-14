package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"commons/mmcapabilities"
	"dexMmController/internal/bitcoin"
	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/pretrade"
	"dexMmController/internal/tradeflow"
)

// TradeFlowRunner executes a full bid-side CEX trade cycle.
type TradeFlowRunner interface {
	Run(ctx context.Context, order tradeflow.Order) (*tradeflow.Result, error)
}

type poolRefillReconciler interface {
	ReconcilePoolRefill(ctx context.Context, transactionID uint) (*db.Transaction, error)
}

// tradeflowRequest is the JSON body for POST /api/cex/tradeflow.
type tradeflowRequest struct {
	Provider               string                   `json:"provider"`
	MarketSide             string                   `json:"market_side"`
	BTCAmount              float64                  `json:"btc_amount"`
	FiatCurrency           string                   `json:"fiat_currency"`
	RequiredCapital        float64                  `json:"required_capital"`
	RequiredBTCLiquidity   float64                  `json:"required_btc_liquidity"`
	SecurityDepositBTC     float64                  `json:"security_deposit_btc"`
	HedgeVenue             string                   `json:"hedge_venue"`
	HedgeSide              string                   `json:"hedge_side"`
	HedgeNotionalBTC       float64                  `json:"hedge_notional_btc"`
	Leverage               float64                  `json:"leverage"`
	OfferID                string                   `json:"offer_id"`
	Dex                    string                   `json:"dex"`
	FiatAmount             float64                  `json:"fiat_amount"`
	TransactionID          uint                     `json:"transaction_id,omitempty"`
	SelectedOffer          json.RawMessage          `json:"selected_offer,omitempty"`
	PaymentMethod          string                   `json:"payment_method"`
	PaymentMethods         []string                 `json:"payment_methods"`
	TradeMode              string                   `json:"trade_mode"`
	ResolvedRoute          *tradeflow.ResolvedRoute `json:"resolved_route"`
	RoutingFeeAllowanceSat int64                    `json:"routing_fee_allowance_sat"`
	CEXWithdrawalFeeBTC    float64                  `json:"cex_withdrawal_fee_btc"`
	SwapSafetyHeadroomBTC  float64                  `json:"swap_safety_headroom_btc"`
}

func (s *Server) handleCexTradeflow(w http.ResponseWriter, r *http.Request) {
	if s.tradeFlow == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("tradeflow is not configured"))
		return
	}

	var req tradeflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.ResolvedRoute == nil && len(req.SelectedOffer) > 0 {
		var selected struct {
			Metadata struct {
				ResolvedRoute *tradeflow.ResolvedRoute `json:"resolved_route"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(req.SelectedOffer, &selected); err == nil {
			req.ResolvedRoute = selected.Metadata.ResolvedRoute
		}
	}
	if req.ResolvedRoute == nil || !req.ResolvedRoute.ValidAuthoritativeSnapshot() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("resolved_route from unified API is required"))
		return
	}
	if err := validateLegacyTradeflowFields(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ResolvedRoute != nil {
		if strings.TrimSpace(req.Provider) == "" {
			req.Provider = req.ResolvedRoute.Provider
		}
		if strings.TrimSpace(req.MarketSide) == "" {
			req.MarketSide = req.ResolvedRoute.MarketSide
		}
		if strings.TrimSpace(req.TradeMode) == "" {
			req.TradeMode = req.ResolvedRoute.TradeMode
		}
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider is required"))
		return
	}
	canonical, err := cexaccount.CanonicalProvider(provider)
	if err != nil || !cexaccount.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(cexaccount.SupportedCEXProviders(), ", ")))
		return
	}
	tradeMode, err := pretrade.ParseTradeMode(req.TradeMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	marketSide := normalizeMarketSide(req.MarketSide)
	if !strings.EqualFold(req.ResolvedRoute.Provider, provider) ||
		!strings.EqualFold(req.ResolvedRoute.MarketSide, marketSide) ||
		!strings.EqualFold(req.ResolvedRoute.TradeMode, tradeMode) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("resolved_route does not match the requested trade"))
		return
	}
	// The route snapshot is authoritative for every CEX-side execution input.
	req.BTCAmount = req.ResolvedRoute.BTCAmount
	req.FiatCurrency = req.ResolvedRoute.FiatCurrency
	req.FiatAmount = req.ResolvedRoute.FiatAmount
	req.RequiredCapital = req.ResolvedRoute.RequiredCapital
	req.RequiredBTCLiquidity = req.ResolvedRoute.RequiredBTCLiquidity
	req.SecurityDepositBTC = req.ResolvedRoute.SecurityDepositBTC
	req.HedgeVenue = req.ResolvedRoute.HedgeVenue
	req.HedgeSide = req.ResolvedRoute.HedgeSide
	req.HedgeNotionalBTC = req.ResolvedRoute.HedgeNotional
	req.Leverage = req.ResolvedRoute.Leverage
	req.RoutingFeeAllowanceSat = req.ResolvedRoute.RoutingFeeAllowanceSat
	req.CEXWithdrawalFeeBTC = req.ResolvedRoute.CEXWithdrawalFeeBTC
	req.SwapSafetyHeadroomBTC = req.ResolvedRoute.SwapSafetyHeadroomBTC
	if req.ResolvedRoute.NeedsLightning && req.RequiredBTCLiquidity <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("required_btc_liquidity must be greater than zero for Swap_main2lightning routes"))
		return
	}
	if tradeMode == pretrade.TradeModeHedgelessDoubleCollateral {
		// Keep this public boundary aligned with tradeflow.validate. The duplicate
		// checks give clients a 400 before any execution object is invoked, while
		// TradeFlow still protects non-HTTP callers.
		if req.RequiredBTCLiquidity <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("required_btc_liquidity must be greater than zero in hedgeless mode"))
			return
		}
		if marketSide != "bid" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("hedgeless execution is supported only for bid-side offers"))
			return
		}
		switch strings.ToLower(strings.TrimSpace(req.Dex)) {
		case "bisq", "hodlhodl", "robosats":
		default:
			writeError(w, http.StatusBadRequest, fmt.Errorf("hedgeless execution is not supported for dex %q", req.Dex))
			return
		}
		if canonical != "Binance" && canonical != "Kraken" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("hedgeless pool refill is not supported for provider %q", provider))
			return
		}
	} else {
		if strings.TrimSpace(req.HedgeVenue) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("hedge_venue is required"))
			return
		}
		if req.HedgeNotionalBTC <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("hedge_notional_btc must be greater than zero"))
			return
		}
	}
	if err := s.requireCEXCapabilities(r.Context(), canonical, tradeMode, req.HedgeVenue); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	lightningSource, err := s.lightningLiquiditySource(r.Context(), req.ResolvedRoute.Settlement)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}

	result, err := s.tradeFlow.Run(r.Context(), tradeflow.Order{
		Provider:                 provider,
		MarketSide:               marketSide,
		BTCAmount:                req.BTCAmount,
		FiatCurrency:             req.FiatCurrency,
		RequiredCapital:          req.RequiredCapital,
		RequiredBTCLiquidity:     req.RequiredBTCLiquidity,
		SecurityDepositBTC:       req.SecurityDepositBTC,
		HedgeVenue:               req.HedgeVenue,
		HedgeSide:                req.HedgeSide,
		HedgeNotionalBTC:         req.HedgeNotionalBTC,
		Leverage:                 req.Leverage,
		OfferID:                  strings.TrimSpace(req.OfferID),
		Dex:                      strings.TrimSpace(req.Dex),
		FiatAmount:               req.FiatAmount,
		TransactionID:            req.TransactionID,
		SelectedOffer:            req.SelectedOffer,
		PaymentMethod:            strings.TrimSpace(req.PaymentMethod),
		PaymentMethods:           req.PaymentMethods,
		TradeMode:                tradeMode,
		ResolvedRoute:            req.ResolvedRoute,
		RoutingFeeAllowanceSat:   req.RoutingFeeAllowanceSat,
		CEXWithdrawalFeeBTC:      req.CEXWithdrawalFeeBTC,
		SwapSafetyHeadroomBTC:    req.SwapSafetyHeadroomBTC,
		LightningLiquiditySource: lightningSource,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// TradeFlow persists each step itself (including the transaction id on result);
	// the handler no longer writes the row after the fact.
	writeJSON(w, http.StatusOK, result)
}

func validateLegacyTradeflowFields(req tradeflowRequest) error {
	r := req.ResolvedRoute
	if r == nil {
		return fmt.Errorf("resolved_route from unified API is required")
	}
	mismatch := func(name string) error { return fmt.Errorf("legacy field %s does not match resolved_route", name) }
	if req.Provider != "" && !strings.EqualFold(req.Provider, r.Provider) {
		return mismatch("provider")
	}
	if req.MarketSide != "" && normalizeMarketSide(req.MarketSide) != r.MarketSide {
		return mismatch("market_side")
	}
	if req.BTCAmount != 0 && req.BTCAmount != r.BTCAmount {
		return mismatch("btc_amount")
	}
	if req.FiatCurrency != "" && !strings.EqualFold(req.FiatCurrency, r.FiatCurrency) {
		return mismatch("fiat_currency")
	}
	if req.FiatAmount != 0 && req.FiatAmount != r.FiatAmount {
		return mismatch("fiat_amount")
	}
	if req.RequiredCapital != 0 && req.RequiredCapital != r.RequiredCapital {
		return mismatch("required_capital")
	}
	if req.RequiredBTCLiquidity != 0 && req.RequiredBTCLiquidity != r.RequiredBTCLiquidity {
		return mismatch("required_btc_liquidity")
	}
	if req.SecurityDepositBTC != 0 && req.SecurityDepositBTC != r.SecurityDepositBTC {
		return mismatch("security_deposit_btc")
	}
	if req.HedgeVenue != "" && req.HedgeVenue != r.HedgeVenue {
		return mismatch("hedge_venue")
	}
	if req.HedgeSide != "" && req.HedgeSide != r.HedgeSide {
		return mismatch("hedge_side")
	}
	if req.HedgeNotionalBTC != 0 && req.HedgeNotionalBTC != r.HedgeNotional {
		return mismatch("hedge_notional_btc")
	}
	if req.Leverage != 0 && req.Leverage != r.Leverage {
		return mismatch("leverage")
	}
	if req.TradeMode != "" && req.TradeMode != r.TradeMode {
		return mismatch("trade_mode")
	}
	if req.RoutingFeeAllowanceSat != 0 && req.RoutingFeeAllowanceSat != r.RoutingFeeAllowanceSat {
		return mismatch("routing_fee_allowance_sat")
	}
	if req.CEXWithdrawalFeeBTC != 0 && req.CEXWithdrawalFeeBTC != r.CEXWithdrawalFeeBTC {
		return mismatch("cex_withdrawal_fee_btc")
	}
	if req.SwapSafetyHeadroomBTC != 0 && req.SwapSafetyHeadroomBTC != r.SwapSafetyHeadroomBTC {
		return mismatch("swap_safety_headroom_btc")
	}
	return nil
}

func (s *Server) requireCEXCapabilities(ctx context.Context, provider, tradeMode, hedgeVenue string) error {
	if s.capabilities == nil {
		// Production wiring always installs the store. Keeping it optional makes
		// isolated handler tests and recovery-only tools independent of Postgres.
		return nil
	}
	spot, err := s.capabilities.State(ctx, provider)
	if err != nil {
		return fmt.Errorf("load spot capability policy: %w", err)
	}
	if err := mmcapabilities.RequireSpot(spot); err != nil {
		return err
	}
	if tradeMode == cexaccount.TradeModeHedgelessDoubleCollateral {
		return nil
	}
	definition, ok := mmcapabilities.ProviderForHedgeVenue(hedgeVenue)
	if !ok {
		return fmt.Errorf("unknown hedge venue %q", hedgeVenue)
	}
	hedge, err := s.capabilities.State(ctx, definition.ID)
	if err != nil {
		return fmt.Errorf("load hedge capability policy: %w", err)
	}
	return mmcapabilities.RequireHedge(hedge)
}

func (s *Server) handlePoolRefillReconcile(w http.ResponseWriter, r *http.Request) {
	// This endpoint does not force a state transition. The reconciler first reads
	// LND and releases the singleton pool reservation only when the saved target
	// balance has actually been restored.
	reconciler, ok := s.tradeFlow.(poolRefillReconciler)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("pool refill reconciliation is not configured"))
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid transaction id"))
		return
	}
	tx, err := reconciler.ReconcilePoolRefill(r.Context(), uint(id))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

// preTradeCheckRequest is the JSON body for POST /api/cex/pre-trade-check.
type preTradeCheckRequest struct {
	Provider               string                   `json:"provider"`
	FiatCurrency           string                   `json:"fiat_currency"`
	RequiredCapital        float64                  `json:"required_capital"`
	PaymentMethod          string                   `json:"payment_method"`
	PaymentMethods         []string                 `json:"payment_methods"`
	Dex                    string                   `json:"dex"`
	MarketSide             string                   `json:"market_side"`
	Settlement             string                   `json:"settlement"`
	TradeMode              string                   `json:"trade_mode"`
	HedgeVenue             string                   `json:"hedge_venue"`
	RequiredBTCLiquidity   float64                  `json:"required_btc_liquidity"`
	RoutingFeeAllowanceSat int64                    `json:"routing_fee_allowance_sat"`
	ResolvedRoute          *tradeflow.ResolvedRoute `json:"resolved_route"`
}

// handleCexPreTradeCheck runs pre-trade checks (read-only) and reports the
// per-check results, so the UI can show what passes/fails before the operator
// confirms. It places no order.
func (s *Server) handleCexPreTradeCheck(w http.ResponseWriter, r *http.Request) {
	if s.cexAccount == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cex account service is not configured"))
		return
	}

	var req preTradeCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.ResolvedRoute == nil || !req.ResolvedRoute.ValidAuthoritativeSnapshot() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("resolved_route from unified API is required"))
		return
	}
	if err := validateLegacyPreTradeFields(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	route := req.ResolvedRoute
	provider := strings.TrimSpace(route.Provider)
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider is required"))
		return
	}
	canonical, err := cexaccount.CanonicalProvider(provider)
	if err != nil || !cexaccount.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(cexaccount.SupportedCEXProviders(), ", ")))
		return
	}
	tradeMode, err := pretrade.ParseTradeMode(route.TradeMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	marketSide := normalizeMarketSide(route.MarketSide)
	dex := strings.TrimSpace(req.Dex)
	if route.Settlement == "onchain_swap" && route.RequiredBTCLiquidity <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("required_btc_liquidity must be greater than zero for Swap_main2lightning routes"))
		return
	}
	if route.Settlement == "onchain_swap" && route.RoutingFeeAllowanceSat <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("routing_fee_allowance_sat must be greater than zero for Swap_main2lightning routes"))
		return
	}
	if err := s.requireCEXCapabilities(r.Context(), canonical, tradeMode, route.HedgeVenue); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	lightningSource, err := s.lightningLiquiditySource(r.Context(), route.Settlement)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}

	report, err := s.cexAccount.PreTradeCheck(r.Context(), pretrade.Params{
		Provider:                 provider,
		FiatCurrency:             route.FiatCurrency,
		RequiredCapital:          route.RequiredCapital,
		PaymentMethod:            strings.TrimSpace(req.PaymentMethod),
		PaymentMethods:           req.PaymentMethods,
		Dex:                      dex,
		MarketSide:               marketSide,
		Settlement:               route.Settlement,
		TradeMode:                tradeMode,
		HedgeVenue:               strings.TrimSpace(route.HedgeVenue),
		RequiredBTCLiquidity:     route.RequiredBTCLiquidity,
		RoutingFeeAllowanceSat:   route.RoutingFeeAllowanceSat,
		LightningLiquiditySource: lightningSource,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// lightningLiquiditySource reads the operator's selected liquidity source only
// for routes that require a Lightning payment. Non-Lightning routes deliberately
// do not consult channel configuration and therefore cannot be blocked by it.
func (s *Server) lightningLiquiditySource(ctx context.Context, settlement string) (string, error) {
	if settlement != "onchain_swap" {
		return "", nil
	}
	if s.transactionalLiquiditySettingsRepo == nil {
		return pretrade.LightningLiquidityOwnChannels, nil
	}
	settings, err := s.transactionalLiquiditySettingsRepo.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("load lightning liquidity source: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(settings.LightningLiquiditySource)) {
	case db.TransactionalLiquidityLSPChannel:
		return pretrade.LightningLiquidityLSPChannel, nil
	case db.TransactionalLiquidityOwnChannels, "":
		return pretrade.LightningLiquidityOwnChannels, nil
	default:
		return "", fmt.Errorf("unsupported lightning liquidity source %q", settings.LightningLiquiditySource)
	}
}

func validateLegacyPreTradeFields(req preTradeCheckRequest) error {
	r := req.ResolvedRoute
	if r == nil {
		return fmt.Errorf("resolved_route from unified API is required")
	}
	mismatch := func(name string) error { return fmt.Errorf("legacy field %s does not match resolved_route", name) }
	if req.Provider != "" && !strings.EqualFold(req.Provider, r.Provider) {
		return mismatch("provider")
	}
	if req.FiatCurrency != "" && !strings.EqualFold(req.FiatCurrency, r.FiatCurrency) {
		return mismatch("fiat_currency")
	}
	if req.RequiredCapital != 0 && req.RequiredCapital != r.RequiredCapital {
		return mismatch("required_capital")
	}
	if req.MarketSide != "" && normalizeMarketSide(req.MarketSide) != r.MarketSide {
		return mismatch("market_side")
	}
	if req.Settlement != "" && req.Settlement != r.Settlement {
		return mismatch("settlement")
	}
	if req.TradeMode != "" && req.TradeMode != r.TradeMode {
		return mismatch("trade_mode")
	}
	if req.HedgeVenue != "" && req.HedgeVenue != r.HedgeVenue {
		return mismatch("hedge_venue")
	}
	if req.RequiredBTCLiquidity != 0 && req.RequiredBTCLiquidity != r.RequiredBTCLiquidity {
		return mismatch("required_btc_liquidity")
	}
	if req.RoutingFeeAllowanceSat != 0 && req.RoutingFeeAllowanceSat != r.RoutingFeeAllowanceSat {
		return mismatch("routing_fee_allowance_sat")
	}
	return nil
}

func normalizeMarketSide(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "ask") {
		return "ask"
	}
	return "bid"
}

// WithCexAccount attaches the CEX account overview service.
func (s *Server) WithCexAccount(service *cexaccount.Service) *Server {
	s.cexAccount = service
	return s
}

// WithTradeFlow attaches the bid-side trade orchestrator.
func (s *Server) WithTradeFlow(runner TradeFlowRunner) *Server {
	s.tradeFlow = runner
	return s
}

func (s *Server) handleCexDepositAddresses(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, "deposit addresses", func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.DepositAddresses(ctx, provider)
	})
}

func (s *Server) handleCexWithdrawalAddresses(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, "withdrawal addresses", func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.WithdrawalAddresses(ctx, provider)
	})
}

func (s *Server) handleCexBalances(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, "balances", func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.Balances(ctx, provider)
	})
}

func (s *Server) handleCexHedgePositions(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, "hedge positions", func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.HedgePositions(ctx, provider)
	})
}

func (s *Server) handleCexSpotTrades(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, "spot trades", func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.SpotTrades(ctx, provider, queryInt(r, "limit", 50))
	})
}

func (s *Server) handleCexProviderRequest(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	run func(context.Context, string) (any, error),
) {
	if s.cexAccount == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cex account service is not configured"))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider query parameter is required"))
		return
	}
	canonical, err := cexaccount.CanonicalProvider(provider)
	if err != nil || !cexaccount.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(cexaccount.SupportedCEXProviders(), ", ")))
		return
	}

	payload, err := run(r.Context(), provider)
	if err != nil {
		writeCEXProviderFailure(w, canonical, operation, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func writeCEXProviderFailure(w http.ResponseWriter, provider, operation string, cause error) {
	accessErr := cexaccount.NewAccessError(provider, operation, cause)
	log.Printf("CEX %s %s request failed: %v", provider, operation, cause)
	writeCEXAccessError(w, http.StatusBadGateway, accessErr)
}

func writeCEXAccessError(w http.ResponseWriter, status int, err *cexaccount.AccessError) {
	writeJSON(w, status, map[string]string{
		"error":     err.Error(),
		"code":      "cex_api_access_failed",
		"provider":  err.Provider,
		"operation": err.Operation,
	})
}

// withdrawalTrackResponse merges CEX withdrawal status with optional mempool tx data.
type withdrawalTrackResponse struct {
	cexaccount.WithdrawalStatus
	Mempool *bitcoin.TxStatus `json:"mempool,omitempty"`
}

func (s *Server) handleCexWithdrawalTrack(w http.ResponseWriter, r *http.Request) {
	if s.cexAccount == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cex account service is not configured"))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider query parameter is required"))
		return
	}
	canonical, err := cexaccount.CanonicalProvider(provider)
	if err != nil || !cexaccount.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(cexaccount.SupportedCEXProviders(), ", ")))
		return
	}

	withdrawalID := strings.TrimSpace(r.URL.Query().Get("withdrawal_id"))
	if withdrawalID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("withdrawal_id query parameter is required"))
		return
	}
	clientRequestID := strings.TrimSpace(r.URL.Query().Get("client_request_id"))

	status, err := s.cexAccount.GetWithdrawalStatus(r.Context(), provider, withdrawalID, clientRequestID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	resp := withdrawalTrackResponse{WithdrawalStatus: *status}
	if status.TxHash != "" {
		fetcher := bitcoin.NewNetworkDataFetcher()
		if tx, txErr := fetcher.FetchTransaction(r.Context(), status.TxHash); txErr == nil {
			resp.Mempool = tx
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
