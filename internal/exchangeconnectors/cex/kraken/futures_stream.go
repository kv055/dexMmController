package kraken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"dexMmController/internal/exchangeconnectors/cex"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	defaultFuturesWSURL = "wss://futures.kraken.com/ws/v1"
	hedgeInstrument     = "PI_XBTUSD"
)

// StreamPositions connects to the Kraken Futures WebSocket, subscribes to the
// authenticated open_positions feed (plus the public ticker feed for the live
// funding rate), and emits a normalized cex.HedgePositionUpdate for PI_XBTUSD on
// every update. It owns its own reconnect/backoff loop and runs until ctx is
// cancelled, at which point the returned channel is closed.
//
// On a disconnect it first emits a Connected:false snapshot so subscribers can
// surface the outage, then reconnects with exponential backoff.
func (c *FuturesClient) StreamPositions(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	if strings.TrimSpace(c.apiKey) == "" || strings.TrimSpace(c.apiSecret) == "" {
		return nil, fmt.Errorf("kraken futures: missing API credentials (set KRAKEN_FUTURES_API_KEY/SECRET)")
	}
	out := make(chan cex.HedgePositionUpdate, 8)
	go func() {
		defer close(out)
		backoff := time.Second
		for {
			err := c.streamSession(ctx, out)
			if ctx.Err() != nil {
				return
			}
			emitDisconnected(ctx, out, "kraken_futures", hedgeInstrument, err)
			log.Printf("kraken futures position stream disconnected: %v; reconnecting in %s", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
	return out, nil
}

func (c *FuturesClient) streamSession(ctx context.Context, out chan<- cex.HedgePositionUpdate) error {
	wsURL := defaultFuturesWSURL
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "client shutdown")
	conn.SetReadLimit(4 << 20)

	// 1) Request an auth challenge.
	if err := wsjson.Write(ctx, conn, map[string]any{
		"event":   "challenge",
		"api_key": c.apiKey,
	}); err != nil {
		return fmt.Errorf("kraken futures: request challenge: %w", err)
	}

	// 2) Read frames until we get the challenge, then subscribe to the feeds.
	subscribed := false
	var signed, original string
	// funding holds the last funding rate seen on the ticker feed, merged into
	// each open_positions snapshot.
	funding := 0.0

	for {
		var frame map[string]any
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}

		event, _ := frame["event"].(string)
		feed, _ := frame["feed"].(string)

		switch {
		case event == "challenge":
			original, _ = frame["message"].(string)
			signed = c.signChallenge(original)
			if err := c.subscribeFeeds(ctx, conn, original, signed); err != nil {
				return err
			}
			subscribed = true
		case event == "error":
			msg, _ := frame["message"].(string)
			return fmt.Errorf("kraken futures ws error: %s", msg)
		case feed == "ticker" && subscribed:
			if rate, ok := floatFromAny(frame["funding_rate"]); ok {
				funding = rate
			}
		case feed == "open_positions" || feed == "open_positions_snapshot":
			update, ok := positionUpdateFromKrakenFrame(frame, funding)
			if !ok {
				continue
			}
			select {
			case out <- update:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (c *FuturesClient) subscribeFeeds(ctx context.Context, conn *websocket.Conn, original, signed string) error {
	if err := wsjson.Write(ctx, conn, map[string]any{
		"event":              "subscribe",
		"feed":               "open_positions",
		"api_key":            c.apiKey,
		"original_challenge": original,
		"signed_challenge":   signed,
	}); err != nil {
		return fmt.Errorf("kraken futures: subscribe open_positions: %w", err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{
		"event":       "subscribe",
		"feed":        "ticker",
		"product_ids": []string{hedgeInstrument},
	}); err != nil {
		return fmt.Errorf("kraken futures: subscribe ticker: %w", err)
	}
	return nil
}

// signChallenge produces the signed challenge string Kraken Futures expects for
// authenticated WS feeds: base64(HMAC-SHA512(base64decode(secret), SHA256(challenge))).
// This mirrors the REST signing in signedPost.
func (c *FuturesClient) signChallenge(challenge string) string {
	hash := sha256.Sum256([]byte(challenge))
	secretBytes, err := base64.StdEncoding.DecodeString(c.apiSecret)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha512.New, secretBytes)
	mac.Write(hash[:])
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// positionUpdateFromKrakenFrame translates a Kraken Futures open_positions frame into
// the common schema, picking the PI_XBTUSD position. funding is the latest funding
// rate from the ticker feed (0 if not yet seen). It returns ok=false when the frame
// carries no PI_XBTUSD position (a flat snapshot still yields a Side:"flat" update).
func positionUpdateFromKrakenFrame(frame map[string]any, funding float64) (cex.HedgePositionUpdate, bool) {
	positions, ok := frame["positions"].([]any)
	if !ok {
		return cex.HedgePositionUpdate{}, false
	}

	update := cex.HedgePositionUpdate{
		Venue:       "kraken_futures",
		Instrument:  hedgeInstrument,
		Side:        "flat",
		FundingRate: funding,
		Currency:    "USD",
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		Connected:   true,
	}

	for _, p := range positions {
		pos, ok := p.(map[string]any)
		if !ok {
			continue
		}
		instrument, _ := pos["instrument"].(string)
		if !strings.EqualFold(instrument, hedgeInstrument) {
			continue
		}
		balance, _ := floatFromAny(pos["balance"])
		update.SizeBTC = absFloat(balance)
		update.Side = sideFromSignedSize(balance)
		update.EntryPrice, _ = floatFromAny(pos["entry_price"])
		update.MarkPrice, _ = floatFromAny(pos["mark_price"])
		update.LiquidationPrice, _ = floatFromAny(pos["liquidation_threshold"])
		update.UnrealizedPnL, _ = floatFromAny(pos["pnl"])
		update.Margin, _ = floatFromAny(pos["initial_margin"])
		if cur, ok := pos["pnl_currency"].(string); ok && cur != "" {
			update.Currency = cur
		}
		return update, true
	}
	// No matching position in this frame: the hedge is flat.
	return update, true
}

func floatFromAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	default:
		return 0, false
	}
}

func sideFromSignedSize(size float64) string {
	switch {
	case size > 0:
		return "long"
	case size < 0:
		return "short"
	default:
		return "flat"
	}
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// emitDisconnected pushes a Connected:false snapshot (best-effort, non-blocking on
// ctx cancellation) so subscribers can render an outage banner.
func emitDisconnected(ctx context.Context, out chan<- cex.HedgePositionUpdate, venue, instrument string, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	update := cex.HedgePositionUpdate{
		Venue:      venue,
		Instrument: instrument,
		Side:       "flat",
		Currency:   "USD",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Connected:  false,
		Error:      msg,
	}
	select {
	case out <- update:
	case <-ctx.Done():
	}
}
