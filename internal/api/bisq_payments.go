package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"commons/paymentmethods"

	"dexMmController/internal/exchangeconnectors/dex/bisq"
)

const (
	maxBisqCreateBodyBytes = 64 << 10
	maxBisqFormBytes       = 64 << 10
	maxBisqFormFields      = 48
	maxBisqFieldBytes      = 2048
)

var (
	bisqMethodIDPattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)
	bisqCurrencyPattern = regexp.MustCompile(`^[A-Z0-9]{3,8}$`)
	bisqSaltPattern     = regexp.MustCompile(`^[0-9A-Fa-f]*$`)
)

// bisqPaymentClient is the daemon mutation/read subset used by these handlers.
type bisqPaymentClient interface {
	GetPaymentAccounts(ctx context.Context) ([]bisq.PaymentAccount, error)
	GetPaymentMethods(ctx context.Context) ([]bisq.PaymentMethod, error)
	GetPaymentAccountForm(ctx context.Context, paymentMethodID string) (string, error)
	CreatePaymentAccount(ctx context.Context, paymentAccountForm string) (*bisq.PaymentAccount, error)
	CreateSepaPaymentAccount(ctx context.Context, params bisq.SepaAccountParams) (*bisq.PaymentAccount, error)
	DeletePaymentAccount(ctx context.Context, id string) error
}

type bisqPaymentAccountDTO struct {
	ID              string `json:"id"`
	AccountName     string `json:"account_name"`
	PaymentMethodID string `json:"payment_method_id"`
	TradeCurrency   string `json:"trade_currency"`
}

type bisqPaymentMethodDTO struct {
	ID             string `json:"id"`
	MaxTradePeriod int64  `json:"max_trade_period"`
	MaxTradeLimit  int64  `json:"max_trade_limit"`
}

type bisqPaymentAccountFormDTO struct {
	PaymentMethodID string            `json:"payment_method_id"`
	Fields          map[string]string `json:"fields"`
	Supported       bool              `json:"supported"`
	UnsupportedWhy  string            `json:"unsupported_reason,omitempty"`
}

func toBisqPaymentAccountDTO(a bisq.PaymentAccount) bisqPaymentAccountDTO {
	return bisqPaymentAccountDTO{a.ID, a.AccountName, a.PaymentMethodID, a.TradeCurrency}
}

// The generic request carries the exact camelCase fields advertised by Bisq. The legacy
// snake_case SEPA fields remain accepted for existing clients.
type bisqCreatePaymentAccountRequest struct {
	PaymentMethodID   string                     `json:"payment_method_id"`
	PaymentMethodType string                     `json:"payment_method_type"`
	Fields            map[string]json.RawMessage `json:"fields"`

	AccountName string `json:"account_name"`
	HolderName  string `json:"holder_name"`
	IBAN        string `json:"iban"`
	BIC         string `json:"bic"`
	CountryCode string `json:"country_code"`
}

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
	for _, account := range accounts {
		out = append(out, toBisqPaymentAccountDTO(account))
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_accounts": out})
}

func (s *Server) handleBisqPaymentMethods(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment methods are unavailable"))
		return
	}
	methods, err := s.bisqPayments.GetPaymentMethods(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]bisqPaymentMethodDTO, 0, len(methods))
	for _, method := range methods {
		out = append(out, bisqPaymentMethodDTO{
			ID: method.ID, MaxTradePeriod: method.MaxTradePeriod, MaxTradeLimit: method.MaxTradeLimit,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_methods": out})
}

func (s *Server) handleBisqPaymentAccountForm(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment account forms are unavailable"))
		return
	}
	methodID, err := normalizeBisqMethodID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	templateJSON, err := s.bisqPayments.GetPaymentAccountForm(r.Context(), methodID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	fields, err := parseBisqFormTemplate(templateJSON, methodID)
	if err != nil {
		writeJSON(w, http.StatusOK, bisqPaymentAccountFormDTO{
			PaymentMethodID: methodID,
			Fields:          map[string]string{},
			Supported:       false,
			UnsupportedWhy:  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, bisqPaymentAccountFormDTO{
		PaymentMethodID: methodID, Fields: fields, Supported: true,
	})
}

func (s *Server) handleBisqCreatePaymentAccount(w http.ResponseWriter, r *http.Request) {
	if s.bisqPayments == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq payment accounts are unavailable"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBisqCreateBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req bisqCreatePaymentAccountRequest
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("request body must contain one JSON object"))
		return
	}

	methodID, err := requestBisqMethodID(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var account *bisq.PaymentAccount
	if req.Fields == nil {
		account, err = s.createLegacySepaAccount(r.Context(), methodID, req)
	} else {
		account, err = s.createGenericBisqAccount(r.Context(), methodID, req.Fields)
	}
	if err != nil {
		var validationErr bisqFormValidationError
		if errors.As(err, &validationErr) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, http.StatusBadGateway, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, toBisqPaymentAccountDTO(*account))
}

func (s *Server) createLegacySepaAccount(ctx context.Context, methodID string, req bisqCreatePaymentAccountRequest) (*bisq.PaymentAccount, error) {
	if methodID != "SEPA" {
		return nil, formValidationError("generic payment methods must provide a fields object")
	}
	return s.createSepaAccount(ctx, req.AccountName, req.HolderName, req.IBAN, req.BIC, req.CountryCode)
}

func (s *Server) createGenericBisqAccount(ctx context.Context, methodID string, submitted map[string]json.RawMessage) (*bisq.PaymentAccount, error) {
	templateJSON, err := s.bisqPayments.GetPaymentAccountForm(ctx, methodID)
	if err != nil {
		return nil, fmt.Errorf("get fresh Bisq payment account form: %w", err)
	}
	template, err := parseBisqFormTemplate(templateJSON, methodID)
	if err != nil {
		return nil, formValidationError("Bisq form is not safely supported: %v", err)
	}
	merged, err := mergeBisqForm(template, methodID, submitted)
	if err != nil {
		return nil, err
	}

	// Bisq 1.10.2's stock form parser cannot correctly construct SEPA accounts.
	if methodID == "SEPA" {
		return s.createSepaAccount(ctx,
			merged["accountName"], merged["holderName"], merged["iban"], merged["bic"], merged["country"])
	}

	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode Bisq payment account form: %w", err)
	}
	return s.bisqPayments.CreatePaymentAccount(ctx, string(encoded))
}

func (s *Server) createSepaAccount(ctx context.Context, accountName, holderName, iban, bic, countryCode string) (*bisq.PaymentAccount, error) {
	holderName = strings.TrimSpace(holderName)
	iban = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(iban), " ", ""))
	bic = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(bic), " ", ""))
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	accountName = strings.TrimSpace(accountName)
	if holderName == "" || iban == "" || bic == "" || countryCode == "" {
		return nil, formValidationError("holder_name, iban, bic and country_code are required")
	}
	if err := paymentmethods.ValidateIBAN(iban); err != nil {
		return nil, bisqFormValidationError{err.Error()}
	}
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(countryCode) {
		return nil, formValidationError("country_code must be a two-letter code")
	}
	if accountName == "" {
		accountName = "SEPA " + iban
	}
	return s.bisqPayments.CreateSepaPaymentAccount(ctx, bisq.SepaAccountParams{
		AccountName: accountName, HolderName: holderName, IBAN: iban, BIC: bic, CountryCode: countryCode,
	})
}

type bisqFormValidationError struct{ message string }

func (e bisqFormValidationError) Error() string { return e.message }

func formValidationError(format string, args ...any) error {
	return bisqFormValidationError{fmt.Sprintf(format, args...)}
}

func normalizeBisqMethodID(value string) (string, error) {
	methodID := strings.ToUpper(strings.TrimSpace(value))
	if !bisqMethodIDPattern.MatchString(methodID) {
		return "", formValidationError("invalid payment method id")
	}
	return methodID, nil
}

func requestBisqMethodID(req bisqCreatePaymentAccountRequest) (string, error) {
	id := strings.TrimSpace(req.PaymentMethodID)
	legacyID := strings.TrimSpace(req.PaymentMethodType)
	if id != "" && legacyID != "" && !strings.EqualFold(id, legacyID) {
		return "", formValidationError("payment method IDs do not match")
	}
	if id == "" {
		id = legacyID
	}
	if id == "" && req.Fields == nil {
		id = "SEPA"
	}
	return normalizeBisqMethodID(id)
}

func parseBisqFormTemplate(raw, requestedMethodID string) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > maxBisqFormBytes || !utf8.ValidString(raw) {
		return nil, errors.New("bisq returned an invalid or oversized form")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var form map[string]json.RawMessage
	if err := decoder.Decode(&form); err != nil || len(form) == 0 || len(form) > maxBisqFormFields {
		return nil, errors.New("bisq returned an unsupported form shape")
	}
	out := make(map[string]string, len(form)-1)
	for key, value := range form {
		if key == "_COMMENTS_" {
			continue
		}
		if key == "" || len(key) > 80 {
			return nil, errors.New("bisq form contains an invalid field name")
		}
		var fieldValue string
		if err := json.Unmarshal(value, &fieldValue); err != nil {
			return nil, fmt.Errorf("bisq form field %q is not a string", key)
		}
		if len(fieldValue) > maxBisqFieldBytes {
			return nil, fmt.Errorf("bisq form field %q is oversized", key)
		}
		out[key] = fieldValue
	}
	templateMethodID, ok := out["paymentMethodId"]
	if !ok || templateMethodID != requestedMethodID {
		return nil, errors.New("bisq form payment method does not match the request")
	}
	if _, ok := out["salt"]; !ok {
		return nil, errors.New("bisq form is missing salt")
	}
	return out, nil
}

func mergeBisqForm(template map[string]string, methodID string, submitted map[string]json.RawMessage) (map[string]string, error) {
	for key := range submitted {
		if key == "_COMMENTS_" {
			continue // Descriptive daemon comments are never sent back to Bisq.
		}
		if _, ok := template[key]; !ok {
			return nil, formValidationError("unknown payment account field %q", key)
		}
	}

	merged := make(map[string]string, len(template))
	for key, templateValue := range template {
		if key == "paymentMethodId" {
			if raw, ok := submitted[key]; ok {
				value, err := decodeBisqFormValue(key, raw)
				if err != nil {
					return nil, err
				}
				if value != methodID {
					return nil, formValidationError("payment method ID in fields does not match request")
				}
			}
			merged[key] = methodID
			continue
		}

		raw, provided := submitted[key]
		if !provided {
			if key == "salt" {
				merged[key] = templateValue
				continue
			}
			return nil, formValidationError("payment account field %q is required", key)
		}
		value, err := decodeBisqFormValue(key, raw)
		if err != nil {
			return nil, err
		}
		merged[key] = value
	}
	return merged, nil
}

func decodeBisqFormValue(key string, raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", formValidationError("payment account field %q must be a string", key)
	}
	value = strings.TrimSpace(value)
	if len(value) > maxBisqFieldBytes || !utf8.ValidString(value) {
		return "", formValidationError("payment account field %q is oversized or malformed", key)
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) >= 0 {
		return "", formValidationError("payment account field %q contains control characters", key)
	}
	switch key {
	case "paymentMethodId":
		return normalizeBisqMethodID(value)
	case "salt":
		if len(value) > 128 || len(value)%2 != 0 || !bisqSaltPattern.MatchString(value) {
			return "", formValidationError("salt must be an even-length hexadecimal value")
		}
	case "country":
		value = strings.ToUpper(value)
		if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(value) {
			return "", formValidationError("country must be a two-letter code")
		}
	case "selectedTradeCurrency":
		value = strings.ToUpper(value)
		if !bisqCurrencyPattern.MatchString(value) {
			return "", formValidationError("selectedTradeCurrency is malformed")
		}
	case "tradeCurrencies":
		parts := strings.Split(value, ",")
		for i := range parts {
			parts[i] = strings.ToUpper(strings.TrimSpace(parts[i]))
			if !bisqCurrencyPattern.MatchString(parts[i]) {
				return "", formValidationError("tradeCurrencies contains a malformed currency")
			}
		}
		value = strings.Join(parts, ",")
	case "accountName":
		if value == "" || len(value) > 200 {
			return "", formValidationError("accountName is required and must be at most 200 bytes")
		}
	}
	return value, nil
}

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
