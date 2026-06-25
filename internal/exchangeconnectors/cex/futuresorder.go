package cex

import "encoding/json"

// FuturesOrderResult is the normalized result returned by any futures PlaceOrder call.
type FuturesOrderResult struct {
	Venue      string          `json:"venue"`
	Instrument string          `json:"instrument"`
	Side       string          `json:"side"`
	OrderID    string          `json:"order_id"`
	Status     string          `json:"status"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// HedgePositionUpdate is one normalized snapshot of a perpetual hedge position,
// translated from each venue's native feed. Prices/PnL/margin are in Currency
// (the venue's quote currency, typically USD).
type HedgePositionUpdate struct {
	Venue            string  `json:"venue"`      // kraken_futures|binance_futures|coinbase_futures
	Instrument       string  `json:"instrument"` // PI_XBTUSD|BTCUSDT|BTC-PERP
	Side             string  `json:"side"`       // long|short|flat
	SizeBTC          float64 `json:"size_btc"`
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	LiquidationPrice float64 `json:"liquidation_price,omitempty"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	Margin           float64 `json:"margin"`
	FundingRate      float64 `json:"funding_rate,omitempty"`
	Currency         string  `json:"currency"`
	UpdatedAt        string  `json:"updated_at"`      // RFC3339
	Connected        bool    `json:"connected"`       // upstream feed health
	Error            string  `json:"error,omitempty"` // last upstream error, if any
}
