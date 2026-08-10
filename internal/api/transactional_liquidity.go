package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"dexMmController/internal/db"
)

type transactionalLiquiditySettingsBackend interface {
	Get(context.Context) (db.TransactionalLiquiditySettings, error)
	Update(context.Context, db.TransactionalLiquiditySettingsUpdate) (db.TransactionalLiquiditySettings, error)
}

type transactionalLiquidityUpdateRequest struct {
	FiatCurrency string   `json:"fiat_currency"`
	AmountSource string   `json:"amount_source"`
	CustomAmount *float64 `json:"custom_amount"`
	TradeMode    string   `json:"trade_mode"`
}

func (s *Server) WithTransactionalLiquiditySettings(repo transactionalLiquiditySettingsBackend) *Server {
	s.transactionalLiquiditySettingsRepo = repo
	return s
}

func (s *Server) handleTransactionalLiquiditySettings(w http.ResponseWriter, r *http.Request) {
	if s.transactionalLiquiditySettingsRepo == nil {
		http.Error(w, "transactional liquidity settings not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveTransactionalLiquiditySettings(w, r)
	case http.MethodPut:
		s.updateTransactionalLiquiditySettings(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveTransactionalLiquiditySettings(w http.ResponseWriter, r *http.Request) {
	row, err := s.transactionalLiquiditySettingsRepo.Get(r.Context())
	if err != nil {
		if errors.Is(err, db.ErrTransactionalLiquiditySettingsNotFound) {
			http.Error(w, "transactional liquidity settings not found", http.StatusNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) updateTransactionalLiquiditySettings(w http.ResponseWriter, r *http.Request) {
	var req transactionalLiquidityUpdateRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	updated, err := s.transactionalLiquiditySettingsRepo.Update(r.Context(), db.TransactionalLiquiditySettingsUpdate{
		FiatCurrency: strings.TrimSpace(req.FiatCurrency),
		AmountSource: strings.TrimSpace(req.AmountSource),
		CustomAmount: req.CustomAmount,
		TradeMode:    strings.TrimSpace(req.TradeMode),
	})
	if err != nil {
		var validation db.TransactionalLiquidityValidationError
		if errors.As(err, &validation) {
			http.Error(w, validation.Error(), http.StatusBadRequest)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
