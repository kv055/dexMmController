package dexaccount

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexTakerController/internal/exchangeconnectors/dex/bisq2"
)

func TestResolveDepositBisqIsImmediateNoOp(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	address, invoice, err := taker.ResolveDeposit(context.Background(), "bisq", map[string]any{"exchange": "bisq"})
	if err != nil {
		t.Fatalf("ResolveDeposit: %v", err)
	}
	if address != "" || invoice != "" {
		t.Fatalf("Bisq destination = (%q, %q), want empty direct-route result", address, invoice)
	}
}

func TestResolveDepositStillRejectsNilExecution(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	if _, _, err := taker.ResolveDeposit(context.Background(), "bisq", nil); err == nil {
		t.Fatal("expected nil execution payload error")
	}
}

func TestResolveDepositRoboSatsHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	taker := NewTaker(nil, nil, nil, nil, nil)
	_, _, err := taker.ResolveDeposit(ctx, "robosats", map[string]any{"position_id": "position-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveDeposit error = %v, want context.Canceled", err)
	}
}

func TestTakeBisqRequiresPaymentAccountID(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	_, err := taker.Take(context.Background(), OfferInput{Exchange: "bisq", OfferID: "offer-1", BTCAmount: 0.01})
	if err == nil || !strings.Contains(err.Error(), "payment_account_id") {
		t.Fatalf("error = %v, want missing payment_account_id", err)
	}
}

func TestTakeUnsupportedWithoutBisq2Client(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	_, err := taker.Take(context.Background(), OfferInput{Exchange: "bisq2", OfferID: "o1", FiatAmount: 100, BTCAmount: 0.01, FiatCurrency: "EUR", PaymentMethod: "SEPA"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want bisq2 not configured", err)
	}
}

func TestFormatBisq2PaymentAccount(t *testing.T) {
	got := formatBisq2PaymentAccount("SEPA", map[string]string{
		"owner_name":        "Ada",
		"details_encrypted": `{"iban":"DE00"}`,
		"email":             "a@example.com",
	})
	if !strings.Contains(got, "SEPA") || !strings.Contains(got, "Ada") || !strings.Contains(got, "DE00") {
		t.Fatalf("formatted account = %q", got)
	}
}

func TestTakeBisq2PostsTradeAndPaymentAccount(t *testing.T) {
	var takeBody, eventBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/offerbook/markets/EUR/offers"):
			_, _ = w.Write([]byte(`[{
				"quoteSidePaymentMethods":["SEPA_INSTANT"],
				"baseSidePaymentMethods":["MAIN_CHAIN"],
				"bisqEasyOffer":{"id":"offer-9"}
			}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/trades":
			if err := json.NewDecoder(r.Body).Decode(&takeBody); err != nil {
				t.Errorf("decode take: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"tradeId":"trade-9"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/trades/trade-9/event":
			if err := json.NewDecoder(r.Body).Decode(&eventBody); err != nil {
				t.Errorf("decode event: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	client := bisq2.NewClient(srv.URL, "", srv.Client())
	taker := NewTaker(nil, nil, nil, nil, nil).WithBisq2(client)
	got, err := taker.Take(context.Background(), OfferInput{
		Exchange: "bisq2", OfferID: "offer-9", FiatAmount: 100, BTCAmount: 0.01,
		FiatCurrency: "EUR", PaymentMethod: "SEPA Instant",
	})
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if got.Execution["trade_id"] != "trade-9" {
		t.Fatalf("execution = %#v", got.Execution)
	}
	if takeBody["fiatPaymentMethod"] != "SEPA_INSTANT" {
		t.Fatalf("take fiat rail = %v", takeBody["fiatPaymentMethod"])
	}
	if takeBody["bitcoinPaymentMethod"] != "MAIN_CHAIN" {
		t.Fatalf("take bitcoin rail = %v", takeBody["bitcoinPaymentMethod"])
	}
	if eventBody != nil {
		t.Fatalf("expected no payment-account event without a repo, got %#v", eventBody)
	}
}

func TestResolveDepositBisq2RequiresTradeID(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil).WithBisq2(bisq2.NewClient("http://localhost", "", nil))
	_, _, err := taker.ResolveDeposit(context.Background(), "bisq2", map[string]any{"exchange": "bisq2"})
	if err == nil || !strings.Contains(err.Error(), "trade_id") {
		t.Fatalf("error = %v, want missing trade_id", err)
	}
}

func TestReportBtcSentNoopsForOtherVenues(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	if err := taker.ReportBtcSent(context.Background(), "hodlhodl", "t1", "txid"); err != nil {
		t.Fatalf("ReportBtcSent: %v", err)
	}
}
