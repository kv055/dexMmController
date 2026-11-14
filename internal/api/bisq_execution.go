package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dexTakerController/internal/bisqexec"
	"dexTakerController/internal/db"
	"dexTakerController/internal/dexaccount"
)

type bisqQuoteRequest struct {
	OfferID            string            `json:"offer_id"`
	BTCAmountSats      uint64            `json:"btc_amount_sats"`
	BTCAmount          float64           `json:"btc_amount"`
	TradeMode          string            `json:"trade_mode"`
	PaymentMethod      string            `json:"payment_method"`
	PaymentMethods     []string          `json:"payment_methods"`
	FiatCurrency       string            `json:"fiat_currency"`
	Provider           string            `json:"provider"`
	ResolvedRoute      *db.ResolvedRoute `json:"resolved_route"`
	SelectedOffer      json.RawMessage   `json:"selected_offer"`
	MaxCexFiatSpend    float64           `json:"max_cex_fiat_spend"`
	MaxWalletDebitSats uint64            `json:"max_wallet_debit_sats"`
	MinFiatProceeds    float64           `json:"min_fiat_proceeds"`
}

type bisqMappingRequest struct {
	PaymentMethodName    string `json:"payment_method_name"`
	Currency             string `json:"currency"`
	BisqPaymentAccountID string `json:"bisq_payment_account_id"`
	LocalAccountID       int64  `json:"local_account_id"`
}

type bisqFundingDestinationRequest struct {
	Address string `json:"address"`
}

func isBisqV1(dex string) bool {
	return strings.EqualFold(strings.TrimSpace(dex), "bisq")
}

func (s *Server) handleBisqExecutionAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuoteID        string `json:"quote_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	s.acceptBisqTradeflow(w, r, tradeflowRequest{QuoteID: req.QuoteID, IdempotencyKey: req.IdempotencyKey})
}

func (s *Server) handleBisqExecutionQuote(w http.ResponseWriter, r *http.Request) {
	if s.bisqExec == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("automated Bisq execution is not configured"))
		return
	}
	var req bisqQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.BTCAmountSats == 0 && req.BTCAmount > 0 {
		req.BTCAmountSats = uint64(dexaccount.BTCToSats(req.BTCAmount))
	}
	result, err := s.bisqExec.Quote(r.Context(), bisqexec.QuoteRequest{
		OfferID:            req.OfferID,
		BTCAmountSats:      req.BTCAmountSats,
		TradeMode:          req.TradeMode,
		PaymentMethod:      req.PaymentMethod,
		PaymentMethods:     req.PaymentMethods,
		FiatCurrency:       req.FiatCurrency,
		Provider:           req.Provider,
		ResolvedRoute:      req.ResolvedRoute,
		SelectedOffer:      req.SelectedOffer,
		MaxCexFiatSpend:    req.MaxCexFiatSpend,
		MaxWalletDebitSats: req.MaxWalletDebitSats,
		MinFiatProceeds:    req.MinFiatProceeds,
	})
	if err != nil {
		writeBisqExecError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBisqPaymentAccountMappings(w http.ResponseWriter, r *http.Request) {
	repo := s.currentRepo()
	if repo == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database is not ready"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := repo.ListBisqPaymentAccountMappings(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if rows == nil {
			rows = []db.BisqPaymentAccountMapping{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"mappings": rows})
	case http.MethodPut:
		var req bisqMappingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		localID := req.LocalAccountID
		if localID <= 0 {
			method, err := repo.GetPaymentMethodByName(r.Context(), req.PaymentMethodName)
			if err != nil {
				writeError(w, http.StatusBadGateway, err)
				return
			}
			if method == nil || method.AccountID == nil || *method.AccountID <= 0 {
				writeError(w, http.StatusBadRequest, fmt.Errorf("payment method %q is not linked to a local account", req.PaymentMethodName))
				return
			}
			localID = *method.AccountID
		}
		row, err := repo.UpsertBisqPaymentAccountMapping(r.Context(), db.BisqPaymentAccountMapping{
			LocalAccountID:       localID,
			PaymentMethodName:    req.PaymentMethodName,
			Currency:             req.Currency,
			BisqPaymentAccountID: req.BisqPaymentAccountID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, row)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleBisqFundingDestination(w http.ResponseWriter, r *http.Request) {
	repo := s.currentRepo()
	if repo == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database is not ready"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		row, err := repo.ActiveBisqFundingDestination(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"destination": row})
	case http.MethodPut:
		var req bisqFundingDestinationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.bisqWallet != nil {
			if err := s.bisqWallet.RequireFundingAddress(r.Context(), req.Address); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		row, err := repo.SetActiveBisqFundingDestination(r.Context(), req.Address)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, row)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeBisqExecError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, bisqexec.ErrAutomatedDisabled):
		writeError(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, bisqexec.ErrQuoteExpired), errors.Is(err, bisqexec.ErrIdempotencyReuse), errors.Is(err, db.ErrBisqConflict), errors.Is(err, db.ErrBisqExpired):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, db.ErrBisqQuoteNotFound):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}
