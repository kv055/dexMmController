package coinbase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/exchangeconnectors/cex"
)

const (
	hedgeInstrument  = "BTC-PERP"
	positionPollRate = 2500 * time.Millisecond
)

// coinbaseINTXPosition is the relevant subset of an INTX position payload.
type coinbaseINTXPosition struct {
	Symbol           string `json:"symbol"`
	NetSize          string `json:"net_size"`
	EntryVWAP        string `json:"entry_vwap"`
	MarkPrice        string `json:"mark_price"`
	UnrealizedPnL    string `json:"unrealized_pnl"`
	LiquidationPrice string `json:"liquidation_price"`
	IMContribution   string `json:"im_contribution"`
}

// StreamPositions polls the Coinbase INTX positions endpoint (~2.5s) and emits a
// normalized cex.HedgePositionUpdate for BTC-PERP. Coinbase INTX has no reliable
// authenticated position-push WebSocket, so this REST poll feeds the same common
// schema as the native-WS venues. It runs until ctx is cancelled; the returned
// channel is then closed.
func (c *FuturesClient) StreamPositions(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	if strings.TrimSpace(c.apiKey) == "" || strings.TrimSpace(c.apiSecret) == "" {
		return nil, fmt.Errorf("coinbase futures: missing API credentials (set COINBASE_FUTURES_API_KEY/SECRET)")
	}
	out := make(chan cex.HedgePositionUpdate, 8)
	go func() {
		defer close(out)
		ticker := time.NewTicker(positionPollRate)
		defer ticker.Stop()

		// Poll immediately, then on each tick.
		c.pollOnce(ctx, out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.pollOnce(ctx, out)
			}
		}
	}()
	return out, nil
}

func (c *FuturesClient) pollOnce(ctx context.Context, out chan<- cex.HedgePositionUpdate) {
	update, err := c.fetchPosition(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("coinbase futures position poll failed: %v", err)
		emitDisconnected(ctx, out, err)
		return
	}
	select {
	case out <- update:
	case <-ctx.Done():
	}
}

func (c *FuturesClient) fetchPosition(ctx context.Context) (cex.HedgePositionUpdate, error) {
	portfolioID, err := c.fetchPortfolioID(ctx)
	if err != nil {
		return cex.HedgePositionUpdate{}, fmt.Errorf("portfolio: %w", err)
	}
	path := "/api/v1/portfolios/" + portfolioID + "/positions"
	raw, err := c.signedGet(ctx, path)
	if err != nil {
		return cex.HedgePositionUpdate{}, fmt.Errorf("positions: %w", err)
	}
	return positionUpdateFromCoinbase(raw)
}

// positionUpdateFromCoinbase translates an INTX positions payload into the common
// schema, picking the BTC-PERP position. A flat/absent position yields Side:"flat".
// It accepts either a bare array or an object with a "positions" array.
func positionUpdateFromCoinbase(raw []byte) (cex.HedgePositionUpdate, error) {
	positions, err := decodeCoinbasePositions(raw)
	if err != nil {
		return cex.HedgePositionUpdate{}, err
	}

	update := cex.HedgePositionUpdate{
		Venue:      "coinbase_futures",
		Instrument: hedgeInstrument,
		Side:       "flat",
		Currency:   "USD",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Connected:  true,
	}

	for _, pos := range positions {
		if !strings.EqualFold(pos.Symbol, hedgeInstrument) {
			continue
		}
		netSize := parseCBFloat(pos.NetSize)
		update.SizeBTC = absFloat(netSize)
		update.Side = sideFromSignedSize(netSize)
		update.EntryPrice = parseCBFloat(pos.EntryVWAP)
		update.MarkPrice = parseCBFloat(pos.MarkPrice)
		update.UnrealizedPnL = parseCBFloat(pos.UnrealizedPnL)
		update.LiquidationPrice = parseCBFloat(pos.LiquidationPrice)
		update.Margin = parseCBFloat(pos.IMContribution)
		return update, nil
	}
	return update, nil
}

func decodeCoinbasePositions(raw []byte) ([]coinbaseINTXPosition, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var arr []coinbaseINTXPosition
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var wrapped struct {
		Positions []coinbaseINTXPosition `json:"positions"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Positions, nil
}

func parseCBFloat(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
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
		Venue:      "coinbase_futures",
		Instrument: hedgeInstrument,
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
