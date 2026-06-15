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
