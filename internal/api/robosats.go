package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"dexTakerController/internal/exchangeconnectors/dex/robosats"
)

// Authenticated internal endpoint: only coordinator-native, read-only terms are
// returned. Route selection and quote persistence remain owned by Unified API.
func (s *Server) handleRoboSatsQuote(w http.ResponseWriter, r *http.Request) {
	quoter, ok := s.tradeFlow.(interface {
		QuoteRoboSats(context.Context, robosats.QuoteInput) (*robosats.NativeTerms, error)
	})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("RoboSats quoting is unavailable"))
		return
	}
	var input robosats.QuoteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	terms, err := quoter.QuoteRoboSats(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, terms)
}
