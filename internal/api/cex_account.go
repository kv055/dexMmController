package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/cexaccount"
	"dexMmController/internal/tradeflow"
)

// TradeFlowRunner executes a full bid-side CEX trade cycle.
type TradeFlowRunner interface {
	Run(ctx context.Context, order tradeflow.Order) (*tradeflow.Result, error)
}

// tradeflowRequest is the JSON body for POST /api/cex/tradeflow.
type tradeflowRequest struct {
	Provider           string          `json:"provider"`
	MarketSide         string          `json:"market_side"`
	BTCAmount          float64         `json:"btc_amount"`
	FiatCurrency       string          `json:"fiat_currency"`
	RequiredCapitalEUR float64         `json:"required_capital_eur"`
	HedgeVenue         string          `json:"hedge_venue"`
	HedgeSide          string          `json:"hedge_side"`
	HedgeNotionalBTC   float64         `json:"hedge_notional_btc"`
	Leverage           float64         `json:"leverage"`
	OfferID            string          `json:"offer_id"`
	Dex                string          `json:"dex"`
	FiatAmount         float64         `json:"fiat_amount"`
	TransactionID      uint            `json:"transaction_id,omitempty"`
	SelectedOffer      json.RawMessage `json:"selected_offer,omitempty"`
	PaymentMethod      string          `json:"payment_method"`
	PaymentMethods     []string        `json:"payment_methods"`
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
	if req.BTCAmount <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("btc_amount must be greater than zero"))
		return
	}
	if req.RequiredCapitalEUR <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("required_capital_eur must be greater than zero"))
		return
	}
	if strings.TrimSpace(req.HedgeVenue) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("hedge_venue is required"))
		return
	}
	if req.HedgeNotionalBTC <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("hedge_notional_btc must be greater than zero"))
		return
	}
	marketSide := strings.TrimSpace(req.MarketSide)
	if marketSide == "" {
		marketSide = "bid"
	}

	result, err := s.tradeFlow.Run(r.Context(), tradeflow.Order{
		Provider:           provider,
		MarketSide:         marketSide,
		BTCAmount:          req.BTCAmount,
		FiatCurrency:       req.FiatCurrency,
		RequiredCapitalEUR: req.RequiredCapitalEUR,
		HedgeVenue:         req.HedgeVenue,
		HedgeSide:          req.HedgeSide,
		HedgeNotionalBTC:   req.HedgeNotionalBTC,
		Leverage:           req.Leverage,
		OfferID:            strings.TrimSpace(req.OfferID),
		Dex:                strings.TrimSpace(req.Dex),
		FiatAmount:         req.FiatAmount,
		TransactionID:      req.TransactionID,
		SelectedOffer:      req.SelectedOffer,
		PaymentMethod:      strings.TrimSpace(req.PaymentMethod),
		PaymentMethods:     req.PaymentMethods,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// TradeFlow persists each step itself (including the transaction id on result);
	// the handler no longer writes the row after the fact.
	writeJSON(w, http.StatusOK, result)
}

// preTradeCheckRequest is the JSON body for POST /api/cex/pre-trade-check.
type preTradeCheckRequest struct {
	Provider           string  `json:"provider"`
	FiatCurrency       string  `json:"fiat_currency"`
	RequiredCapitalEUR float64 `json:"required_capital_eur"`
	PaymentMethod      string  `json:"payment_method"`
	PaymentMethods     []string `json:"payment_methods"`
}

// handleCexPreTradeCheck runs all bid-side pre-trade checks (read-only) and reports
// the per-check results, so the UI can show what passes/fails before the operator
// confirms a market buy. It places no order.
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
	if req.RequiredCapitalEUR <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("required_capital_eur must be greater than zero"))
		return
	}

	report, err := s.cexAccount.PreTradeCheck(r.Context(), provider, req.FiatCurrency, req.RequiredCapitalEUR, strings.TrimSpace(req.PaymentMethod), req.PaymentMethods)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
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
	s.handleCexProviderRequest(w, r, func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.DepositAddresses(ctx, provider)
	})
}

func (s *Server) handleCexWithdrawalAddresses(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.WithdrawalAddresses(ctx, provider)
	})
}

func (s *Server) handleCexBalances(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.Balances(ctx, provider)
	})
}

func (s *Server) handleCexHedgePositions(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.HedgePositions(ctx, provider)
	})
}

func (s *Server) handleCexSpotTrades(w http.ResponseWriter, r *http.Request) {
	s.handleCexProviderRequest(w, r, func(ctx context.Context, provider string) (any, error) {
		return s.cexAccount.SpotTrades(ctx, provider, queryInt(r, "limit", 50))
	})
}

func (s *Server) handleCexProviderRequest(
	w http.ResponseWriter,
	r *http.Request,
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
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
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
