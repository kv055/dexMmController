package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/cexaccount"
)

// spotBuyRequest is the JSON body for POST /api/cex/spot-buy.
type spotBuyRequest struct {
	Provider     string  `json:"provider"`
	BTCAmount    float64 `json:"btc_amount"`
	FiatCurrency string  `json:"fiat_currency"`
}

func (s *Server) handleCexSpotBuy(w http.ResponseWriter, r *http.Request) {
	if s.cexAccount == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cex account service is not configured"))
		return
	}

	var req spotBuyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider is required"))
		return
	}
	canonical, err := accounthealth.CanonicalProvider(provider)
	if err != nil || !accounthealth.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(accounthealth.SupportedCEXProviders(), ", ")))
		return
	}
	if req.BTCAmount <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("btc_amount must be greater than zero"))
		return
	}

	result, err := s.cexAccount.MarketBuyBTC(r.Context(), provider, req.BTCAmount, req.FiatCurrency)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// hedgeOpenRequest is the JSON body for POST /api/cex/hedge-open.
type hedgeOpenRequest struct {
	Venue       string  `json:"venue"`
	Side        string  `json:"side"`
	BTCNotional float64 `json:"btc_notional"`
	Leverage    float64 `json:"leverage"`
}

func (s *Server) handleCexHedgeOpen(w http.ResponseWriter, r *http.Request) {
	if s.cexAccount == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cex account service is not configured"))
		return
	}

	var req hedgeOpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if strings.TrimSpace(req.Venue) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("venue is required"))
		return
	}
	if req.BTCNotional <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("btc_notional must be greater than zero"))
		return
	}

	result, err := s.cexAccount.OpenHedge(r.Context(), req.Venue, req.Side, req.BTCNotional, req.Leverage)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// WithCexAccount attaches the CEX account overview service.
func (s *Server) WithCexAccount(service *cexaccount.Service) *Server {
	s.cexAccount = service
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
	canonical, err := accounthealth.CanonicalProvider(provider)
	if err != nil || !accounthealth.IsCEXProvider(canonical) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(accounthealth.SupportedCEXProviders(), ", ")))
		return
	}

	payload, err := run(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
