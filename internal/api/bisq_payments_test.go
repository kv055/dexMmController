package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexMmController/internal/exchangeconnectors/dex/bisq"
)

type fakeBisqPaymentClient struct {
	accounts    []bisq.PaymentAccount
	createParam bisq.SepaAccountParams
	created     *bisq.PaymentAccount
	createErr   error
	deletedID   string
	deleteErr   error
}

func (f *fakeBisqPaymentClient) GetPaymentAccounts(context.Context) ([]bisq.PaymentAccount, error) {
	return f.accounts, nil
}

func (f *fakeBisqPaymentClient) CreateSepaPaymentAccount(_ context.Context, params bisq.SepaAccountParams) (*bisq.PaymentAccount, error) {
	f.createParam = params
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created != nil {
		return f.created, nil
	}
	return &bisq.PaymentAccount{ID: "acct-1", AccountName: params.AccountName, PaymentMethodID: "SEPA", TradeCurrency: "EUR"}, nil
}

func (f *fakeBisqPaymentClient) DeletePaymentAccount(_ context.Context, id string) error {
	f.deletedID = id
	return f.deleteErr
}

func TestHandleBisqPaymentAccountsUnavailable(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/bisq/payment-accounts", nil)
	rec := httptest.NewRecorder()
	s.handleBisqPaymentAccounts(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleBisqPaymentAccountsLists(t *testing.T) {
	fake := &fakeBisqPaymentClient{accounts: []bisq.PaymentAccount{
		{ID: "a1", AccountName: "SEPA main", PaymentMethodID: "SEPA", TradeCurrency: "EUR"},
	}}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodGet, "/api/bisq/payment-accounts", nil)
	rec := httptest.NewRecorder()
	s.handleBisqPaymentAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		PaymentAccounts []bisqPaymentAccountDTO `json:"payment_accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.PaymentAccounts) != 1 || body.PaymentAccounts[0].ID != "a1" {
		t.Fatalf("unexpected accounts: %+v", body.PaymentAccounts)
	}
}

func TestHandleBisqCreateRejectsNonSepa(t *testing.T) {
	fake := &fakeBisqPaymentClient{}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts",
		strings.NewReader(`{"payment_method_type":"REVOLUT","holder_name":"A","iban":"x","bic":"y","country_code":"DE"}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleBisqCreateRejectsBadIBAN(t *testing.T) {
	fake := &fakeBisqPaymentClient{}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts",
		strings.NewReader(`{"holder_name":"Alice","iban":"DE00 0000","bic":"COBADEFF","country_code":"DE"}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for bad IBAN (%s)", rec.Code, rec.Body.String())
	}
	if fake.createParam.IBAN != "" {
		t.Fatalf("client should not have been called with a bad IBAN, got %q", fake.createParam.IBAN)
	}
}

func TestHandleBisqCreateRequiresFields(t *testing.T) {
	fake := &fakeBisqPaymentClient{}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts",
		strings.NewReader(`{"holder_name":"Alice"}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing fields (%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleBisqCreateSepaSuccess(t *testing.T) {
	fake := &fakeBisqPaymentClient{}
	s := (&Server{}).WithBisqPayments(fake)
	// Valid German IBAN, with spaces to confirm they are stripped.
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts",
		strings.NewReader(`{"account_name":"Main","holder_name":"Alice","iban":"DE89 3704 0044 0532 0130 00","bic":"COBADEFFXXX","country_code":"de"}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if fake.createParam.IBAN != "DE89370400440532013000" {
		t.Fatalf("iban not normalized: %q", fake.createParam.IBAN)
	}
	if fake.createParam.CountryCode != "DE" {
		t.Fatalf("country not upper-cased: %q", fake.createParam.CountryCode)
	}
	if fake.createParam.AccountName != "Main" {
		t.Fatalf("account name = %q", fake.createParam.AccountName)
	}
}

func TestHandleBisqCreateSurfacesDaemonError(t *testing.T) {
	fake := &fakeBisqPaymentClient{createErr: errors.New("bisq: rate limit exceeded")}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts",
		strings.NewReader(`{"holder_name":"Alice","iban":"DE89370400440532013000","bic":"COBADEFFXXX","country_code":"DE"}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rate limit") {
		t.Fatalf("daemon error not surfaced: %s", rec.Body.String())
	}
}

func TestHandleBisqDelete(t *testing.T) {
	fake := &fakeBisqPaymentClient{}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodDelete, "/api/bisq/payment-accounts/acct-9", nil)
	req.SetPathValue("id", "acct-9")
	rec := httptest.NewRecorder()
	s.handleBisqDeletePaymentAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if fake.deletedID != "acct-9" {
		t.Fatalf("deleted id = %q, want acct-9", fake.deletedID)
	}
}
