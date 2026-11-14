package kraken

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dexTakerController/internal/exchangeconnectors/cex"
	"dexTakerController/internal/metrics"
	"github.com/krakenfx/api-go/v2/pkg/callback"
	"github.com/krakenfx/api-go/v2/pkg/derivatives"
	krakenapi "github.com/krakenfx/api-go/v2/pkg/kraken"
)

const hedgeInstrument = "PI_XBTUSD"

// StreamPositions uses Kraken's official Futures WebSocket client for the
// authenticated open_positions and public ticker feeds.
func (c *FuturesClient) StreamPositions(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	if strings.TrimSpace(c.apiKey) == "" || strings.TrimSpace(c.apiSecret) == "" {
		return nil, fmt.Errorf("kraken futures: active database API credentials are missing")
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
			metrics.WSReconnects.WithLabelValues("kraken_futures_positions").Inc()
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
	ws := derivatives.NewWebSocket()
	ws.PublicKey, ws.PrivateKey = c.apiKey, c.apiSecret
	funding := 0.0
	disconnected := make(chan error, 1)
	sessionErr := make(chan error, 1)
	ws.OnDisconnected.Recurring(func(e *callback.Event[error]) {
		signalKrakenStreamError(disconnected, e.Data)
	})
	ws.OnReceived.Recurring(func(e *callback.Event[*krakenapi.WebSocketMessage]) {
		frame, err := e.Data.Map()
		if err != nil {
			signalKrakenStreamError(sessionErr, fmt.Errorf("decode websocket message: %w", err))
			return
		}
		if err := krakenSessionError(frame); err != nil {
			signalKrakenStreamError(sessionErr, err)
			return
		}
		if frame["feed"] == "ticker" {
			if rate, ok := floatFromAny(frame["funding_rate"]); ok {
				funding = rate
			}
			return
		}
		if frame["feed"] != "open_positions" && frame["feed"] != "open_positions_snapshot" {
			return
		}
		if update, ok := positionUpdateFromKrakenFrame(frame, funding); ok {
			select {
			case out <- update:
			case <-ctx.Done():
			}
		}
	})
	if err := ws.Connect(); err != nil {
		return err
	}
	// The connector owns the session lifecycle so a new client can authenticate
	// and resubscribe after every disconnect.
	ws.DoReconnect = false
	defer ws.Disconnect()
	if err := authenticateWithContext(ctx, ws, disconnected); err != nil {
		return fmt.Errorf("kraken futures authenticate: %w", err)
	}
	if err := ws.SubPrivate("open_positions"); err != nil {
		return fmt.Errorf("kraken futures subscribe open positions: %w", err)
	}
	if err := ws.SubTicker(hedgeInstrument); err != nil {
		return fmt.Errorf("kraken futures subscribe ticker: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-disconnected:
		return err
	case err := <-sessionErr:
		return err
	}
}

type krakenAuthentication struct {
	challenge string
	signature string
}

func authenticateWithContext(ctx context.Context, ws *derivatives.WebSocket, disconnected <-chan error) error {
	result := make(chan krakenAuthentication, 1)
	authErr := make(chan error, 1)
	callback := ws.OnReceived.Recurring(func(e *callback.Event[*krakenapi.WebSocketMessage]) {
		frame, err := e.Data.Map()
		if err != nil {
			signalKrakenStreamError(authErr, err)
			return
		}
		if err := krakenSessionError(frame); err != nil {
			signalKrakenStreamError(authErr, err)
			return
		}
		event, _ := frame["event"].(string)
		if event != "challenge" {
			return
		}
		challenge, _ := frame["message"].(string)
		if challenge == "" {
			signalKrakenStreamError(authErr, fmt.Errorf("empty authentication challenge"))
			return
		}
		signature, err := derivatives.Sign(ws.PrivateKey, strings.NewReader(challenge), "", "")
		if err != nil {
			signalKrakenStreamError(authErr, fmt.Errorf("sign authentication challenge: %w", err))
			return
		}
		select {
		case result <- krakenAuthentication{challenge: challenge, signature: signature}:
		default:
		}
	})
	defer ws.OnReceived.Deregister(callback)
	if err := ws.WriteJSON(map[string]any{"event": "challenge", "api_key": ws.PublicKey}); err != nil {
		return fmt.Errorf("request challenge: %w", err)
	}
	timer := time.NewTimer(ws.AuthenticateTimeout)
	defer timer.Stop()
	select {
	case auth := <-result:
		ws.Challenge, ws.Signature = auth.challenge, auth.signature
		ws.OnAuthenticated.Call(auth.challenge)
		return nil
	case err := <-authErr:
		return err
	case err := <-disconnected:
		return fmt.Errorf("disconnected during authentication: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("authentication timed out")
	}
}

func krakenSessionError(frame map[string]any) error {
	event, _ := frame["event"].(string)
	if event != "error" && !strings.HasSuffix(event, "_failed") {
		return nil
	}
	message, _ := frame["message"].(string)
	if message == "" {
		message = "unspecified error"
	}
	return fmt.Errorf("kraken futures websocket %s: %s", event, message)
}

func signalKrakenStreamError(ch chan<- error, err error) {
	if err == nil {
		err = fmt.Errorf("kraken futures websocket disconnected")
	}
	select {
	case ch <- err:
	default:
	}
}

func positionUpdateFromKrakenFrame(frame map[string]any, funding float64) (cex.HedgePositionUpdate, bool) {
	positions, ok := frame["positions"].([]any)
	if !ok {
		return cex.HedgePositionUpdate{}, false
	}
	update := cex.HedgePositionUpdate{Venue: "kraken_futures", Instrument: hedgeInstrument, Side: "flat", FundingRate: funding, Currency: "USD", UpdatedAt: time.Now().UTC().Format(time.RFC3339), Connected: true}
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
		update.SizeBTC, update.Side = absFloat(balance), sideFromSignedSize(balance)
		update.EntryPrice, _ = floatFromAny(pos["entry_price"])
		update.MarkPrice, _ = floatFromAny(pos["mark_price"])
		update.LiquidationPrice, _ = floatFromAny(pos["liquidation_threshold"])
		update.UnrealizedPnL, _ = floatFromAny(pos["pnl"])
		update.Margin, _ = floatFromAny(pos["initial_margin"])
		if currency, ok := pos["pnl_currency"].(string); ok && currency != "" {
			update.Currency = currency
		}
		return update, true
	}
	return update, true
}

func floatFromAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
}
func sideFromSignedSize(size float64) string {
	if size > 0 {
		return "long"
	}
	if size < 0 {
		return "short"
	}
	return "flat"
}
func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func emitDisconnected(ctx context.Context, out chan<- cex.HedgePositionUpdate, venue, instrument string, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	update := cex.HedgePositionUpdate{Venue: venue, Instrument: instrument, Side: "flat", Currency: "USD", UpdatedAt: time.Now().UTC().Format(time.RFC3339), Connected: false, Error: msg}
	select {
	case out <- update:
	case <-ctx.Done():
	}
}
