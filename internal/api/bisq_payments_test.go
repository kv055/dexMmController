package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexTakerController/internal/exchangeconnectors/dex/bisq"
)

type fakeBisqPaymentClient struct {
	accounts    []bisq.PaymentAccount
	methods     []bisq.PaymentMethod
	form        string
	formErr     error
	formMethod  string
	genericForm string
	genericErr  error
	createParam bisq.SepaAccountParams
	created     *bisq.PaymentAccount
	createErr   error
	deletedID   string
	deleteErr   error
}

func (f *fakeBisqPaymentClient) GetPaymentAccounts(context.Context) ([]bisq.PaymentAccount, error) {
	return f.accounts, nil
}

func (f *fakeBisqPaymentClient) GetPaymentMethods(context.Context) ([]bisq.PaymentMethod, error) {
	return f.methods, nil
}

func (f *fakeBisqPaymentClient) GetPaymentAccountForm(_ context.Context, method string) (string, error) {
	f.formMethod = method
	if f.formErr != nil {
		return "", f.formErr
	}
	return f.form, nil
}

func (f *fakeBisqPaymentClient) CreatePaymentAccount(_ context.Context, form string) (*bisq.PaymentAccount, error) {
	f.genericForm = form
	if f.genericErr != nil {
		return nil, f.genericErr
	}
	return &bisq.PaymentAccount{ID: "generic-1", AccountName: "Wallet", PaymentMethodID: "REVOLUT", TradeCurrency: "EUR"}, nil
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

func TestHandleBisqPaymentMethodsListsDaemonMetadata(t *testing.T) {
	fake := &fakeBisqPaymentClient{methods: []bisq.PaymentMethod{
		{ID: "REVOLUT", MaxTradePeriod: 86_400_000, MaxTradeLimit: 100_000_000},
	}}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodGet, "/api/bisq/payment-methods", nil)
	rec := httptest.NewRecorder()
	s.handleBisqPaymentMethods(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"REVOLUT"`) ||
		!strings.Contains(rec.Body.String(), `"max_trade_limit":100000000`) {
		t.Fatalf("status/body = %d %s", rec.Code, rec.Body.String())
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

func TestHandleBisqPaymentAccountFormStripsComments(t *testing.T) {
	fake := &fakeBisqPaymentClient{form: `{
		"_COMMENTS_": ["do not edit"],
		"paymentMethodId": "REVOLUT",
		"accountName": "your accountname",
		"tradeCurrencies": "comma delimited currency code list",
		"selectedTradeCurrency": "primary trading currency code",
		"userName": "your username",
		"salt": ""
	}`}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodGet, "/api/bisq/payment-methods/REVOLUT/form", nil)
	req.SetPathValue("id", "REVOLUT")
	rec := httptest.NewRecorder()
	s.handleBisqPaymentAccountForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "_COMMENTS_") {
		t.Fatalf("comments leaked into response: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"supported":true`) || fake.formMethod != "REVOLUT" {
		t.Fatalf("unexpected response/call: %s method=%q", rec.Body.String(), fake.formMethod)
	}
}

func TestHandleBisqGenericCreateValidatesAndSanitizesFreshTemplate(t *testing.T) {
	fake := &fakeBisqPaymentClient{form: `{
		"_COMMENTS_": ["daemon text"],
		"paymentMethodId": "REVOLUT",
		"accountName": "your accountname",
		"tradeCurrencies": "codes",
		"selectedTradeCurrency": "code",
		"userName": "your username",
		"salt": ""
	}`}
	s := (&Server{}).WithBisqPayments(fake)
	body := `{"payment_method_id":"REVOLUT","fields":{` +
		`"_COMMENTS_":["attacker text"],"paymentMethodId":"REVOLUT",` +
		`"accountName":"Wallet","tradeCurrencies":"eur, usd",` +
		`"selectedTradeCurrency":"eur","userName":"alice","salt":"A0"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(fake.genericForm, "_COMMENTS_") || strings.Contains(fake.genericForm, "attacker") {
		t.Fatalf("comments were submitted to Bisq: %s", fake.genericForm)
	}
	if !strings.Contains(fake.genericForm, `"paymentMethodId":"REVOLUT"`) ||
		!strings.Contains(fake.genericForm, `"selectedTradeCurrency":"EUR"`) ||
		!strings.Contains(fake.genericForm, `"tradeCurrencies":"EUR,USD"`) ||
		!strings.Contains(fake.genericForm, `"salt":"A0"`) {
		t.Fatalf("required fields were not normalized/preserved: %s", fake.genericForm)
	}
}

func TestHandleBisqGenericCreateRejectsUnknownField(t *testing.T) {
	fake := &fakeBisqPaymentClient{form: `{"_COMMENTS_":[],"paymentMethodId":"REVOLUT","accountName":"x","userName":"x","salt":""}`}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
		`{"payment_method_id":"REVOLUT","fields":{"accountName":"Wallet","userName":"alice","admin":true}}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown") {
		t.Fatalf("status/body = %d %s", rec.Code, rec.Body.String())
	}
	if fake.genericForm != "" {
		t.Fatal("generic RPC must not be called for an unknown field")
	}
}

func TestHandleBisqGenericCreateRejectsMethodMismatch(t *testing.T) {
	fake := &fakeBisqPaymentClient{form: `{"_COMMENTS_":[],"paymentMethodId":"REVOLUT","accountName":"x","userName":"x","salt":""}`}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
		`{"payment_method_id":"REVOLUT","fields":{"paymentMethodId":"F2F","accountName":"Wallet","userName":"alice"}}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not match") {
		t.Fatalf("status/body = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBisqGenericCreateRejectsMalformedAndOversizedValues(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "non string", field: "userName", value: `123`},
		{name: "bad currency", field: "selectedTradeCurrency", value: `"EU RO"`},
		{name: "bad salt", field: "salt", value: `"xyz"`},
		{name: "oversized", field: "userName", value: `"` + strings.Repeat("a", maxBisqFieldBytes+1) + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBisqPaymentClient{form: `{
				"_COMMENTS_":[],"paymentMethodId":"REVOLUT","accountName":"x",
				"selectedTradeCurrency":"x","userName":"x","salt":""
			}`}
			s := (&Server{}).WithBisqPayments(fake)
			fields := `"accountName":"Wallet","selectedTradeCurrency":"EUR","userName":"alice","salt":""`
			fields = strings.Replace(fields, `"`+tt.field+`":"alice"`, `"`+tt.field+`":`+tt.value, 1)
			fields = strings.Replace(fields, `"`+tt.field+`":"EUR"`, `"`+tt.field+`":`+tt.value, 1)
			fields = strings.Replace(fields, `"`+tt.field+`":""`, `"`+tt.field+`":`+tt.value, 1)
			req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
				`{"payment_method_id":"REVOLUT","fields":{`+fields+`}}`))
			rec := httptest.NewRecorder()
			s.handleBisqCreatePaymentAccount(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
			}
			if fake.genericForm != "" {
				t.Fatal("generic RPC must not be called for malformed values")
			}
		})
	}
}

func TestHandleBisqGenericSepaUsesCustomRPC(t *testing.T) {
	fake := &fakeBisqPaymentClient{form: `{
		"_COMMENTS_":[],"paymentMethodId":"SEPA","accountName":"x","bic":"x",
		"country":"your two letter country code","holderName":"x","iban":"x","salt":""
	}`}
	s := (&Server{}).WithBisqPayments(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
		`{"payment_method_id":"SEPA","fields":{"accountName":"Main","bic":"COBADEFFXXX",`+
			`"country":"de","holderName":"Alice","iban":"DE89 3704 0044 0532 0130 00","salt":""}}`))
	rec := httptest.NewRecorder()
	s.handleBisqCreatePaymentAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if fake.createParam.IBAN != "DE89370400440532013000" || fake.createParam.CountryCode != "DE" {
		t.Fatalf("SEPA parameters not routed to custom RPC: %+v", fake.createParam)
	}
	if fake.genericForm != "" {
		t.Fatal("stock generic RPC must not be used for SEPA")
	}
}

func TestHandleBisqGenericCreateSurfacesFormAndCreateErrors(t *testing.T) {
	t.Run("fresh form", func(t *testing.T) {
		fake := &fakeBisqPaymentClient{formErr: errors.New("daemon unavailable")}
		s := (&Server{}).WithBisqPayments(fake)
		req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
			`{"payment_method_id":"REVOLUT","fields":{}}`))
		rec := httptest.NewRecorder()
		s.handleBisqCreatePaymentAccount(rec, req)
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "daemon unavailable") {
			t.Fatalf("status/body = %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("create", func(t *testing.T) {
		fake := &fakeBisqPaymentClient{
			form:       `{"_COMMENTS_":[],"paymentMethodId":"REVOLUT","accountName":"x","userName":"x","salt":""}`,
			genericErr: errors.New("duplicate account"),
		}
		s := (&Server{}).WithBisqPayments(fake)
		req := httptest.NewRequest(http.MethodPost, "/api/bisq/payment-accounts", strings.NewReader(
			`{"payment_method_id":"REVOLUT","fields":{"accountName":"Wallet","userName":"alice"}}`))
		rec := httptest.NewRecorder()
		s.handleBisqCreatePaymentAccount(rec, req)
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "duplicate") {
			t.Fatalf("status/body = %d %s", rec.Code, rec.Body.String())
		}
	})
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
