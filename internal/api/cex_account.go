package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/cexaccount"
)

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
