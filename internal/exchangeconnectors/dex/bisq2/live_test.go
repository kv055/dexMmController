package bisq2

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Run with:
//
//	BISQ2_REST_HOST=127.0.0.1:8090 go test -tags live -count=1 -timeout 3m ./internal/exchangeconnectors/dex/bisq2 -run Live
func liveClient(t *testing.T) *Client {
	t.Helper()
	host := strings.TrimSpace(os.Getenv("BISQ2_REST_HOST"))
	if host == "" {
		t.Skip("BISQ2_REST_HOST is unset")
	}
	c := NewClient(host, os.Getenv("BISQ2_API_PASSWORD"), &http.Client{Timeout: 45 * time.Second})
	t.Cleanup(c.Close)
	return c
}

func TestLiveVersion(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	version, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if strings.TrimSpace(version) == "" {
		t.Fatal("empty version")
	}
	t.Logf("bisq2 version %s", version)
}

func TestLiveGetOfferAndRails(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	payload, _, err := c.doJSON(ctx, http.MethodGet, "/offerbook/markets/EUR/offers", nil, 0)
	if err != nil {
		t.Fatalf("list EUR offers: %v", err)
	}
	var items []offerbookItem
	if err := json.Unmarshal(payload, &items); err != nil {
		t.Fatalf("decode offerbook: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("EUR offerbook is empty; daemon may still be syncing")
	}

	var offer *Offer
	for _, item := range items {
		o := item.asOffer()
		if strings.EqualFold(item.BisqEasyOffer.ID, "") && o.ID == "" {
			continue
		}
		if PickBitcoinRail(o.BitcoinRails) == "" || MatchFiatRail("", o.FiatRails) == "" && len(o.FiatRails) == 0 {
			continue
		}
		offer = o
		break
	}
	if offer == nil {
		// Fall back to the first decoded item even if rails are missing.
		offer = items[0].asOffer()
	}
	got, err := c.GetOffer(ctx, "EUR", offer.ID)
	if err != nil {
		t.Fatalf("GetOffer(%s): %v", offer.ID, err)
	}
	if got.ID != offer.ID {
		t.Fatalf("GetOffer id = %q, want %q", got.ID, offer.ID)
	}
	t.Logf("offer %s bitcoin=%v fiat=%v", got.ID, got.BitcoinRails, got.FiatRails)
	if rail := PickBitcoinRail(got.BitcoinRails); rail != BitcoinRailMainChain && rail != BitcoinRailLN {
		t.Fatalf("unexpected bitcoin rail %q from %v", rail, got.BitcoinRails)
	}
}

func TestLiveTakeUnknownOfferIsRejected(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), takeOfferTimeout+10*time.Second)
	defer cancel()

	_, err := c.TakeOffer(ctx, "not-a-real-offer-id", TakeOfferParams{
		BaseSideAmount:       10_000,
		QuoteSideAmount:      100_000,
		BitcoinPaymentMethod: BitcoinRailMainChain,
		FiatPaymentMethod:    "SEPA",
	})
	if err == nil {
		t.Fatal("TakeOffer of unknown id succeeded; refusing to continue")
	}
	t.Logf("unknown take rejected: %v", err)
	if !strings.Contains(strings.ToLower(err.Error()), "status") &&
		!strings.Contains(strings.ToLower(err.Error()), "not found") &&
		!strings.Contains(strings.ToLower(err.Error()), "invalid") &&
		!strings.Contains(strings.ToLower(err.Error()), "no such") &&
		!strings.Contains(strings.ToLower(err.Error()), "unexpected") &&
		!strings.Contains(err.Error(), "NoSuchElement") {
		// Daemon should fail the take; any HTTP/API error is success for this test.
		t.Logf("error did not match common phrases, but take did fail")
	}
}
