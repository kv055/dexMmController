package premiumoffers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/robosats"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	defaultNetProfitThreshold = 2.0
	defaultMinQualifiedOffers = 3
)

// Watcher represents a watcher.
type Watcher struct {
	repo                *db.TransactionRepository
	taker               *dexaccount.Taker
	url                 string
	netProfitThreshold  float64
	minQualifiedOffers  int
	lastLoggedSignature string
}

// Offer is a normalized DEX offer used by this package.
type Offer struct {
	// ID identifies the offer.
	ID int64 `json:"id"`
	// Exchange is the exchange value for the offer.
	Exchange string `json:"exchange"`
	// Direction is the direction value for the offer.
	Direction string `json:"direction"`
	// Price is the price value used by the offer.
	Price float64 `json:"price"`
	// PriceDiffPercent is a percentage value.
	PriceDiffPercent *float64 `json:"price_diff_percent"`
	// TradeCostPercent is a percentage value.
	TradeCostPercent *float64 `json:"trade_cost_percent"`
	// NetPremiumPercent is a percentage value.
	NetPremiumPercent *float64 `json:"net_premium_percent"`
	// BTCMin is a BTC-denominated btcmin value.
	BTCMin *float64 `json:"btc_min"`
	// BTCMax is a BTC-denominated btcmax value.
	BTCMax *float64 `json:"btc_max"`
	// PaymentMethod is the payment method value for the offer.
	PaymentMethod *string `json:"payment_method"`
	// OfferID is the offer id value for the offer.
	OfferID *string `json:"offer_id"`
	// FirstSeen is the first seen value for the offer.
	FirstSeen *string `json:"first_seen"`
	// LastSeen is the last seen value for the offer.
	LastSeen *string `json:"last_seen"`
	// RefPrice is the price value used by the offer.
	RefPrice *float64 `json:"ref_price"`
}

// NewWatcher creates a watcher.
func NewWatcher(
	repo *db.TransactionRepository,
	bisqClient *bisq.Client,
	hodlhodlClient *hodlhodl.Client,
	robosatsClient *robosats.Client,
	streamURL string,
) *Watcher {
	return &Watcher{
		repo:               repo,
		taker:              dexaccount.NewTaker(repo, bisqClient, hodlhodlClient, robosatsClient),
		url:                streamURL,
		netProfitThreshold: defaultNetProfitThreshold,
		minQualifiedOffers: defaultMinQualifiedOffers,
	}
}

// Run runs value.
func (w *Watcher) Run(ctx context.Context) error {
	if w.url == "" {
		return nil
	}

	backoff := time.Second
	for {
		err := w.runSession(ctx)
		if err == nil || ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("premium-offer watcher disconnected: %v; reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (w *Watcher) runSession(ctx context.Context) error {
	log.Printf("connecting to premium offer stream: %s", w.url)
	conn, _, err := websocket.Dial(ctx, w.url, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "client shutdown")
	conn.SetReadLimit(8 << 20)

	for {
		var msg any
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return err
		}

		qualified := qualifiedOffers(msg, w.netProfitThreshold)
		if len(qualified) == 0 {
			continue
		}

		log.Printf("premium-offer watcher found %d offers above %.2f%% net margin", len(qualified), w.netProfitThreshold)
		if len(qualified) < w.minQualifiedOffers {
			continue
		}

		signature := offerSignature(qualified)
		if signature == w.lastLoggedSignature {
			continue
		}

		tx, err := buildTransaction(w.url, qualified)
		if err != nil {
			return fmt.Errorf("building transaction from premium offers: %w", err)
		}
		if err := w.repo.CreateTransaction(tx); err != nil {
			return fmt.Errorf("creating transaction from premium offers: %w", err)
		}
		if err := w.takeBestOffer(ctx, tx, qualified); err != nil {
			log.Printf("tx %d: failed to take best premium offer: %v", tx.ID, err)
		}

		w.lastLoggedSignature = signature
		log.Printf("logged trade candidate tx=%d from %d qualifying premium offers", tx.ID, len(qualified))
	}
}

func qualifiedOffers(v any, threshold float64) []Offer {
	raw := offersWithPremium(v)
	qualified := make([]Offer, 0, len(raw))
	for _, offerMap := range raw {
		netPremium, ok := floatValue(offerMap["net_premium_percent"])
		if !ok || netPremium <= threshold {
			continue
		}

		b, err := json.Marshal(offerMap)
		if err != nil {
			continue
		}

		var offer Offer
		if err := json.Unmarshal(b, &offer); err != nil {
			continue
		}
		qualified = append(qualified, offer)
	}
	return qualified
}

func offersWithPremium(v any) []map[string]any {
	var out []map[string]any
	collectOffersWithPremium(v, &out)
	return out
}

func collectOffersWithPremium(v any, out *[]map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if hasPremiumField(t) {
			*out = append(*out, t)
		}
		for _, child := range t {
			collectOffersWithPremium(child, out)
		}
	case []any:
		for _, item := range t {
			collectOffersWithPremium(item, out)
		}
	}
}

func hasPremiumField(m map[string]any) bool {
	_, hasPremium := m["premium"]
	_, hasPriceDiff := m["price_diff_percent"]
	_, hasNetPremium := m["net_premium_percent"]
	return hasPremium || hasPriceDiff || hasNetPremium
}

func floatValue(v any) (float64, bool) {
	switch value := v.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func offerSignature(offers []Offer) string {
	parts := make([]string, 0, len(offers))
	for _, offer := range offers {
		netPremium := 0.0
		if offer.NetPremiumPercent != nil {
			netPremium = *offer.NetPremiumPercent
		}

		offerID := ""
		if offer.OfferID != nil {
			offerID = *offer.OfferID
		}
		parts = append(parts, fmt.Sprintf("%d|%s|%s|%.2f", offer.ID, offer.Exchange, offerID, netPremium))
	}
	sort.Strings(parts)

	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

func buildTransaction(streamURL string, offers []Offer) (*db.Transaction, error) {
	selected := bestOffer(offers)
	payload, err := json.Marshal(map[string]any{
		"trigger":               "premium_offer_threshold",
		"stream_url":            streamURL,
		"qualified_offer_count": len(offers),
		"qualified_offers":      offers,
		"selected_offer":        selected,
		"logged_at":             time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}

	tx := &db.Transaction{
		Direction:                                strings.ToUpper(selected.Direction),
		FiatCurrency:                             "EUR",
		Cex:                                      inferCexFromStreamURL(streamURL),
		Dex:                                      inferDexPlatform(selected.Exchange),
		Status:                                   db.StatusFiatToCexInitiated,
		DexOfferCreation:                         payload,
		TotalTransactionPnl:                      netPremium(selected),
		PremiumOverCexPriceToBreakEvenPrediction: netPremium(selected),
	}

	if selected.RefPrice != nil && *selected.RefPrice > 0 && selected.BTCMax != nil && *selected.BTCMax > 0 {
		tx.FiatAmount = *selected.RefPrice * *selected.BTCMax
		tx.CexBitcoinAmount = *selected.BTCMax
	} else if selected.BTCMin != nil && *selected.BTCMin > 0 && selected.RefPrice != nil && *selected.RefPrice > 0 {
		tx.FiatAmount = *selected.RefPrice * *selected.BTCMin
		tx.CexBitcoinAmount = *selected.BTCMin
	}

	return tx, nil
}

func bestOffer(offers []Offer) Offer {
	best := offers[0]
	bestNet := netPremium(best)
	for _, offer := range offers[1:] {
		if netPremium(offer) > bestNet {
			best = offer
			bestNet = netPremium(offer)
		}
	}
	return best
}

func netPremium(offer Offer) float64 {
	if offer.NetPremiumPercent == nil {
		return 0
	}
	return *offer.NetPremiumPercent
}

func inferDexPlatform(exchange string) db.DexPlatform {
	switch strings.ToLower(exchange) {
	case "bisq":
		return db.DexBisq
	case "hodlhodl":
		return db.DexHodlHodl
	case "robosats":
		return db.DexRoboSats
	default:
		return db.DexPlatform(exchange)
	}
}

func inferCexFromStreamURL(raw string) db.CexExchange {
	u, err := url.Parse(raw)
	if err != nil {
		return db.CexKraken
	}

	switch strings.ToLower(u.Query().Get("cex")) {
	case "binance":
		return db.CexBinance
	case "coinbase":
		return db.CexCoinbase
	case "bybit":
		return db.CexBybit
	case "okx":
		return db.CexOKX
	case "bitfinex":
		return db.CexBitfinex
	case "gemini":
		return db.CexGemini
	case "bitstamp":
		return db.CexBitStamp
	case "kraken":
		return db.CexKraken
	default:
		return db.CexKraken
	}
}

func (w *Watcher) takeBestOffer(ctx context.Context, tx *db.Transaction, offers []Offer) error {
	selected := bestOffer(offers)
	if selected.OfferID == nil || *selected.OfferID == "" {
		return errorsPayload(tx, "selected offer has no offer_id")
	}
	if w.taker == nil {
		return fmt.Errorf("DEX taker is not configured")
	}

	takeResult, err := w.taker.Take(ctx, dexaccount.OfferInput{
		Exchange:   selected.Exchange,
		OfferID:    *selected.OfferID,
		FiatAmount: offerFiatAmount(selected),
		BTCAmount:  offerBTCAmount(selected),
	})
	if err != nil {
		_ = w.updateExecutionPayload(tx, selected, nil, err)
		return err
	}

	dexAddress := takeResult.DepositAddress
	if dexAddress != "" {
		tx.DexBtcAddress = dexAddress
	}
	return w.updateExecutionPayload(tx, selected, takeResult.Execution, nil)
}

func (w *Watcher) updateExecutionPayload(tx *db.Transaction, selected Offer, result map[string]any, executionErr error) error {
	payload := map[string]any{}
	if len(tx.DexOfferCreation) > 0 {
		if err := json.Unmarshal(tx.DexOfferCreation, &payload); err != nil {
			return fmt.Errorf("decoding DexOfferCreation payload: %w", err)
		}
	}

	payload["selected_offer"] = selected
	payload["execution_attempted_at"] = time.Now().UTC()
	if result != nil {
		payload["execution"] = result
	}
	if executionErr != nil {
		payload["execution_error"] = executionErr.Error()
	} else {
		payload["execution_error"] = nil
	}

	updated, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding DexOfferCreation payload: %w", err)
	}

	tx.DexOfferCreation = updated
	if executionErr == nil {
		tx.Status = db.StatusDexPositionTaken
	}
	return w.repo.UpdateTransaction(tx)
}

func offerFiatAmount(offer Offer) float64 {
	btcAmount := offerBTCAmount(offer)
	if btcAmount > 0 && offer.RefPrice != nil && *offer.RefPrice > 0 {
		return btcAmount * *offer.RefPrice
	}
	return 0
}

func offerBTCAmount(offer Offer) float64 {
	if offer.BTCMax != nil && *offer.BTCMax > 0 {
		return *offer.BTCMax
	}
	if offer.BTCMin != nil && *offer.BTCMin > 0 {
		return *offer.BTCMin
	}
	return 0
}

func errorsPayload(tx *db.Transaction, message string) error {
	if tx == nil {
		return fmt.Errorf(message)
	}
	return fmt.Errorf("tx %d: %s", tx.ID, message)
}
