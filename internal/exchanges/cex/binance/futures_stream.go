package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/exchanges/cex"
)

const (
	defaultFuturesWSBaseURL = "wss://fstream.binance.com"
	hedgeSymbol             = "BTCUSDT"
	listenKeyKeepAlive      = 30 * time.Minute
)

// StreamPositions creates a Binance USDⓈ-M Futures user-data stream (for live
// position/PnL via ACCOUNT_UPDATE) combined with the public btcusdt@markPrice
// stream (for live mark price + funding rate), and emits a normalized
// cex.HedgePositionUpdate on every update. It owns its own reconnect/backoff and
// listenKey keep-alive, running until ctx is cancelled; the returned channel is
// then closed.
func (c *FuturesClient) StreamPositions(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("binance futures: missing API key (set BINANCE_FUTURES_API_KEY)")
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
			emitDisconnected(ctx, out, err)
			log.Printf("binance futures position stream disconnected: %v; reconnecting in %s", err, backoff)
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
	listenKey, err := c.createListenKey(ctx)
	if err != nil {
		return fmt.Errorf("binance futures: create listenKey: %w", err)
	}

	// Keep the listenKey alive for the lifetime of this session.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.keepListenKeyAlive(sessionCtx)

	wsURL := fmt.Sprintf("%s/stream?streams=%s/%s@markPrice@1s",
		defaultFuturesWSBaseURL, listenKey, strings.ToLower(hedgeSymbol))
	return streamCombined(sessionCtx, wsURL, out)
}

// snapshot accumulates the latest values from the two interleaved streams so a
// coherent update can be emitted whenever either stream ticks.
type snapshot struct {
	sizeBTC     float64
	side        string
	entryPrice  float64
	markPrice   float64
	unrealized  float64
	margin      float64
	fundingRate float64
	seenPos     bool
}

func (c *FuturesClient) createListenKey(ctx context.Context) (string, error) {
	raw, err := c.listenKeyRequest(ctx, http.MethodPost)
	if err != nil {
		return "", err
	}
	var resp struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if resp.ListenKey == "" {
		return "", fmt.Errorf("empty listenKey in response: %s", string(raw))
	}
	return resp.ListenKey, nil
}

func (c *FuturesClient) keepListenKeyAlive(ctx context.Context) {
	ticker := time.NewTicker(listenKeyKeepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.listenKeyRequest(ctx, http.MethodPut); err != nil {
				log.Printf("binance futures: listenKey keep-alive failed: %v", err)
			}
		}
	}
}

// listenKeyRequest performs an unsigned listenKey lifecycle call (only the API key
// header is required by Binance for these endpoints).
func (c *FuturesClient) listenKeyRequest(ctx context.Context, method string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/fapi/v1/listenKey", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// applyAccountUpdate merges an ACCOUNT_UPDATE event's BTCUSDT position into snap.
// Returns true if a BTCUSDT position was present.
func applyAccountUpdate(data map[string]any, snap *snapshot) bool {
	account, ok := data["a"].(map[string]any)
	if !ok {
		return false
	}
	positions, ok := account["P"].([]any)
	if !ok {
		return false
	}
	for _, p := range positions {
		pos, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if sym, _ := pos["s"].(string); !strings.EqualFold(sym, hedgeSymbol) {
			continue
		}
		amount := parseStringFloat(pos["pa"])
		snap.sizeBTC = absFloat(amount)
		snap.side = sideFromSignedSize(amount)
		snap.entryPrice = parseStringFloat(pos["ep"])
		snap.unrealized = parseStringFloat(pos["up"])
		snap.margin = parseStringFloat(pos["iw"])
		snap.seenPos = true
		return true
	}
	return false
}

// applyMarkPrice merges a markPriceUpdate event into snap.
func applyMarkPrice(data map[string]any, snap *snapshot) {
	if sym, _ := data["s"].(string); !strings.EqualFold(sym, hedgeSymbol) {
		return
	}
	snap.markPrice = parseStringFloat(data["p"])
	snap.fundingRate = parseStringFloat(data["r"])
}

func updateFromSnapshot(snap snapshot) cex.HedgePositionUpdate {
	side := snap.side
	if side == "" {
		side = "flat"
	}
	return cex.HedgePositionUpdate{
		Venue:         "binance_futures",
		Instrument:    hedgeSymbol,
		Side:          side,
		SizeBTC:       snap.sizeBTC,
		EntryPrice:    snap.entryPrice,
		MarkPrice:     snap.markPrice,
		UnrealizedPnL: snap.unrealized,
		Margin:        snap.margin,
		FundingRate:   snap.fundingRate,
		Currency:      "USDT",
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		Connected:     true,
	}
}

func parseStringFloat(v any) float64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
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

func emitDisconnected(ctx context.Context, out chan<- cex.HedgePositionUpdate, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	update := cex.HedgePositionUpdate{
		Venue:      "binance_futures",
		Instrument: hedgeSymbol,
		Side:       "flat",
		Currency:   "USDT",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Connected:  false,
		Error:      msg,
	}
	select {
	case out <- update:
	case <-ctx.Done():
	}
}
