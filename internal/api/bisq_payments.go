package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"commons/paymentmethods"

	"dexMmController/internal/exchangeconnectors/dex/bisq"
)

// bisqPaymentClient is the subset of the bisq1 gRPC client used to manage the daemon's own
// payment-account profile. It is satisfied by *bisq.Client and kept as an interface so the
// server can be built without the DEX stack (and unit-tested with a fake).
type bisqPaymentClient interface {
	GetPaymentAccounts(ctx context.Context) ([]bisq.PaymentAccount, error)
	CreateSepaPaymentAccount(ctx context.Context, params bisq.SepaAccountParams) (*bisq.PaymentAccount, error)
	DeletePaymentAccount(ctx context.Context, id string) error
}

// bisqPaymentAccountDTO is the JSON shape returned for a bisq payment account. It mirrors
// bisq.PaymentAccount but carries stable json tags for the HTTP API.
type bisqPaymentAccountDTO struct {
	ID              string `json:"id"`
	AccountName     string `json:"account_name"`
	PaymentMethodID string `json:"payment_method_id"`
	TradeCurrency   string `json:"trade_currency"`
}

func toBisqPaymentAccountDTO(a bisq.PaymentAccount) bisqPaymentAccountDTO {
	return bisqPaymentAccountDTO{
		ID:              a.ID,
		AccountName:     a.AccountName,
		PaymentMethodID: a.PaymentMethodID,
		TradeCurrency:   a.TradeCurrency,
	}
}

// bisqCreateSepaRequest is the JSON body for POST /api/bisq/payment-accounts. Only SEPA is
// supported for now; other bisq payment method types would need their own create path.
type bisqCreateSepaRequest struct {
	PaymentMethodType string `json:"payment_method_type"`
	AccountName       string `json:"account_name"`
	HolderName        string `json:"holder_name"`
	IBAN              string `json:"iban"`
	BIC               string `json:"bic"`
	CountryCode       string `json:"country_code"`
}

// handleBisqPaymentAccounts lists the payment accounts saved in the bisq daemon's profile.
func (s *Server) handleBisqPaymentAccounts(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment accounts are unavailable"))
		return
	}
	accounts, err := s.bisqPayments.GetPaymentAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]bisqPaymentAccountDTO, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, toBisqPaymentAccountDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_accounts": out})
}

// handleBisqCreatePaymentAccount pushes one of our payment methods into the bisq daemon's
// profile. Only SEPA (bank transfer) is wired up; other types are rejected with 400.
func (s *Server) handleBisqCreatePaymentAccount(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment accounts are unavailable"))
		return
	}

	var req bisqCreateSepaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	// SEPA is the only supported type for now. Default an empty type to SEPA so the simplest
	// callers work, but reject any explicit non-SEPA type with a clear message.
	methodType := strings.ToUpper(strings.TrimSpace(req.PaymentMethodType))
	if methodType != "" && methodType != "SEPA" {
		writeError(w, http.StatusBadRequest, errors.New("payment method type not yet supported for bisq sync (only SEPA)"))
		return
	}

	holderName := strings.TrimSpace(req.HolderName)
	iban := strings.ReplaceAll(strings.TrimSpace(req.IBAN), " ", "")
	bic := strings.TrimSpace(req.BIC)
	countryCode := strings.ToUpper(strings.TrimSpace(req.CountryCode))
	accountName := strings.TrimSpace(req.AccountName)

	if holderName == "" || iban == "" || bic == "" || countryCode == "" {
		writeError(w, http.StatusBadRequest, errors.New("holder_name, iban, bic and country_code are required"))
		return
	}
	if err := paymentmethods.ValidateIBAN(iban); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if accountName == "" {
		accountName = "SEPA " + iban
	}

	account, err := s.bisqPayments.CreateSepaPaymentAccount(r.Context(), bisq.SepaAccountParams{
		AccountName: accountName,
		HolderName:  holderName,
		IBAN:        iban,
		BIC:         bic,
		CountryCode: countryCode,
	})
	if err != nil {
		// The daemon surfaces validation problems (bad country, rate limit, duplicate) as
		// gRPC errors; pass the message through so the UI can show it.
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, toBisqPaymentAccountDTO(*account))
}

// handleBisqDeletePaymentAccount removes a payment account from the bisq daemon's profile.
func (s *Server) handleBisqDeletePaymentAccount(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment accounts are unavailable"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("payment account id is required"))
		return
	}
	if err := s.bisqPayments.DeletePaymentAccount(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
