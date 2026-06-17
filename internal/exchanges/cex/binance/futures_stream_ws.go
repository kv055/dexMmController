package binance

import (
	"context"

	"dexMmController/internal/exchanges/cex"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// combinedFrame is the envelope Binance uses for combined streams (/stream?streams=).
type combinedFrame struct {
	Stream string         `json:"stream"`
	Data   map[string]any `json:"data"`
}

// streamCombined dials the combined user-data + markPrice stream and emits a merged
// cex.HedgePositionUpdate whenever either underlying stream ticks. It returns when
// the connection drops or ctx is cancelled.
func streamCombined(ctx context.Context, wsURL string, out chan<- cex.HedgePositionUpdate) error {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "client shutdown")
	conn.SetReadLimit(4 << 20)

	var snap snapshot
	for {
		var frame combinedFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		if frame.Data == nil {
			continue
		}

		eventType, _ := frame.Data["e"].(string)
		changed := false
		switch eventType {
		case "ACCOUNT_UPDATE":
			changed = applyAccountUpdate(frame.Data, &snap)
		case "markPriceUpdate":
			applyMarkPrice(frame.Data, &snap)
			changed = true
		}
		if !changed {
			continue
		}

		select {
		case out <- updateFromSnapshot(snap):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
