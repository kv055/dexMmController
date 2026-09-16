package bisq2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTakeOfferPostsQuoteAmountAndRails(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/trades" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"tradeId":"trade-abc"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "", srv.Client())
	got, err := c.TakeOffer(context.Background(), "offer-1", TakeOfferParams{
		BaseSideAmount:       15000000,
		QuoteSideAmount:      1_000_000,
		BitcoinPaymentMethod: BitcoinRailMainChain,
		FiatPaymentMethod:    "SEPA_INSTANT",
	})
	if err != nil {
		t.Fatalf("TakeOffer: %v", err)
	}
	if got.TradeID != "trade-abc" {
		t.Fatalf("TradeID = %q, want trade-abc", got.TradeID)
	}
	if gotBody["offerId"] != "offer-1" {
		t.Fatalf("offerId = %v", gotBody["offerId"])
	}
	if gotBody["quoteSideAmount"] != float64(1_000_000) {
		t.Fatalf("quoteSideAmount = %v", gotBody["quoteSideAmount"])
	}
	if gotBody["bitcoinPaymentMethod"] != BitcoinRailMainChain {
		t.Fatalf("bitcoinPaymentMethod = %v", gotBody["bitcoinPaymentMethod"])
	}
	if gotBody["fiatPaymentMethod"] != "SEPA_INSTANT" {
		t.Fatalf("fiatPaymentMethod = %v", gotBody["fiatPaymentMethod"])
	}
}

func TestGetOfferReadsPresentationRails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/offerbook/markets/EUR/offers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{
				"quoteSidePaymentMethods": ["SEPA", "SEPA_INSTANT"],
				"baseSidePaymentMethods": ["MAIN_CHAIN", "LN"],
				"bisqEasyOffer": {"id": "offer-1"}
			}
		]`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "", srv.Client())
	offer, err := c.GetOffer(context.Background(), "eur", "offer-1")
	if err != nil {
		t.Fatalf("GetOffer: %v", err)
	}
	if PickBitcoinRail(offer.BitcoinRails) != BitcoinRailMainChain {
		t.Fatalf("bitcoin rails = %v", offer.BitcoinRails)
	}
	if MatchFiatRail("SEPA Instant", offer.FiatRails) != "SEPA_INSTANT" {
		t.Fatalf("fiat rails = %v", offer.FiatRails)
	}
}

func TestProcessTradeEventPatchesProtocol(t *testing.T) {
	t.Parallel()
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "", srv.Client())
	if err := c.ConfirmBtcSent(context.Background(), "trade-1", "txid-abc"); err != nil {
		t.Fatalf("ConfirmBtcSent: %v", err)
	}
	if method != http.MethodPatch || path != "/api/v1/trades/trade-1/event" {
		t.Fatalf("got %s %s", method, path)
	}
	if !strings.Contains(body, `"SELLER_CONFIRM_BTC_SENT"`) || !strings.Contains(body, `"txid-abc"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestIngestTradePropertiesCachesBitcoinPaymentData(t *testing.T) {
	t.Parallel()
	c := NewClient("http://localhost", "", nil)
	c.ingestTopicPayload("TRADE_PROPERTIES", `[{"trade-1":{"bitcoinPaymentData":"bc1qdest"}}]`, false)
	if got := c.BitcoinPaymentData("trade-1"); got != "bc1qdest" {
		t.Fatalf("cached data = %q", got)
	}
	got, err := c.WaitForBitcoinPaymentData(context.Background(), "trade-1", time.Second)
	if err != nil {
		t.Fatalf("WaitForBitcoinPaymentData: %v", err)
	}
	if got != "bc1qdest" {
		t.Fatalf("waited data = %q", got)
	}
}

func TestIngestTradesCachesChannelAndInvoice(t *testing.T) {
	t.Parallel()
	c := NewClient("http://localhost", "", nil)
	c.ingestTopicPayload("TRADES", `[{
		"channel":{"id":"chan-9","tradeId":"trade-2"},
		"trade":{"id":"trade-2","bitcoinPaymentData":"lnbc1xyz"}
	}]`, false)
	if got := c.BitcoinPaymentData("trade-2"); got != "lnbc1xyz" {
		t.Fatalf("cached data = %q", got)
	}
	c.mu.RLock()
	channel := c.channelByTrade["trade-2"]
	c.mu.RUnlock()
	if channel != "chan-9" {
		t.Fatalf("channel = %q", channel)
	}
}

func TestQuoteAmountUnits(t *testing.T) {
	t.Parallel()
	if got := QuoteAmountUnits(100); got != 1_000_000 {
		t.Fatalf("100 EUR = %d, want 1000000", got)
	}
	if got := QuoteAmountUnits(12.34); got != 123400 {
		t.Fatalf("12.34 = %d, want 123400", got)
	}
}

func TestWsURLRewritesScheme(t *testing.T) {
	t.Parallel()
	c := NewClient("http://bisq2:8090", "", nil)
	if got := c.wsURL(); got != "ws://bisq2:8090/websocket" {
		t.Fatalf("wsURL = %q", got)
	}
}
