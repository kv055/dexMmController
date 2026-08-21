package binance

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	sharedbinance "commons/cexclient/binance"
	"dexMmController/internal/exchangeconnectors/cex"
	"dexMmController/internal/metrics"
)

const (
	hedgeSymbol          = "BTCUSDT"
	listenKeyKeepAlive   = 30 * time.Minute
	streamHealthInterval = time.Second
)

// StreamPositions creates a Binance USDⓈ-M Futures user-data stream (for live
// position/PnL via ACCOUNT_UPDATE) combined with the public btcusdt@markPrice
// stream (for live mark price + funding rate), and emits a normalized
// cex.HedgePositionUpdate on every update. It owns its own reconnect/backoff and
// listenKey keep-alive, running until ctx is cancelled; the returned channel is
// then closed.
func (c *FuturesClient) StreamPositions(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("binance futures: active database API key is missing")
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
			metrics.WSReconnects.WithLabelValues("binance_futures_positions").Inc()
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

	streams, err := sharedbinance.NewFuturesCombinedStreams(c.options)
	if err != nil {
		return err
	}
	streamNames := []string{listenKey, strings.ToLower(hedgeSymbol) + "@markPrice@1s"}
	if err := streams.Connect("binance-futures-connector", streamNames); err != nil {
		return err
	}
	defer streams.CloseWebSocketStreamConnection()
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if _, err := c.client.RestApi.UserDataStreamsAPI.CloseUserDataStream(closeCtx).Execute(); err != nil {
			log.Printf("binance futures: close listen key failed: %v", err)
		}
	}()

	var snap snapshot
	var snapMu sync.Mutex
	streamErr := make(chan error, 1)
	keepAliveErr := make(chan error, 1)
	go func() { keepAliveErr <- c.keepListenKeyAlive(sessionCtx) }()
	if err := streams.On(listenKey, func(frame map[string]interface{}) {
		data, _ := frame["data"].(map[string]interface{})
		if data == nil {
			data = frame
		}
		if err := binanceUserStreamError(data); err != nil {
			signalStreamError(streamErr, err)
			return
		}
		snapMu.Lock()
		changed := applyAccountUpdate(data, &snap)
		update := updateFromSnapshot(snap)
		snapMu.Unlock()
		if changed {
			select {
			case out <- update:
			case <-sessionCtx.Done():
			}
		}
	}); err != nil {
		return err
	}
	markStream := strings.ToLower(hedgeSymbol) + "@markPrice@1s"
	if err := streams.On(markStream, func(frame map[string]interface{}) {
		data, _ := frame["data"].(map[string]interface{})
		if data == nil {
			data = frame
		}
		snapMu.Lock()
		applyMarkPrice(data, &snap)
		update := updateFromSnapshot(snap)
		snapMu.Unlock()
		select {
		case out <- update:
		case <-sessionCtx.Done():
		}
	}); err != nil {
		return err
	}
	connection := streams.WsCommon.Connections[0]
	healthTicker := time.NewTicker(streamHealthInterval)
	defer healthTicker.Stop()
	for {
		select {
		case <-sessionCtx.Done():
			return sessionCtx.Err()
		case err := <-connection.ErrorChan:
			return err
		case err := <-streamErr:
			return err
		case err := <-keepAliveErr:
			if err != nil {
				return err
			}
		case <-healthTicker.C:
			if !connection.IsHealthy() {
				return fmt.Errorf("binance futures websocket closed")
			}
		}
	}
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
	resp, err := c.client.RestApi.UserDataStreamsAPI.StartUserDataStream(ctx).Execute()
	if err != nil {
		return "", err
	}
	listenKey := resp.Data.GetListenKey()
	if listenKey == "" {
		return "", fmt.Errorf("empty listenKey in response")
	}
	return listenKey, nil
}

func (c *FuturesClient) keepListenKeyAlive(ctx context.Context) error {
	ticker := time.NewTicker(listenKeyKeepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := c.client.RestApi.UserDataStreamsAPI.KeepaliveUserDataStream(ctx).Execute(); err != nil {
				return fmt.Errorf("binance futures listen-key keepalive: %w", err)
			}
		}
	}
}

func binanceUserStreamError(data map[string]any) error {
	event, _ := data["e"].(string)
	if event != "listenKeyExpired" {
		return nil
	}
	return fmt.Errorf("binance futures listen key expired")
}

func signalStreamError(ch chan<- error, err error) {
	if err == nil {
		return
	}
	select {
	case ch <- err:
	default:
	}
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
