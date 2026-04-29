package openclaw

import "encoding/json"

// This is a deliberately tiny message envelope to model a "Gateway-style"
// bi-directional protocol: requests, responses, and (optionally) events.

type reqMsg struct {
	Type   string          `json:"type"` // "req"
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type resMsg struct {
	Type    string          `json:"type"` // "res"
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *errMsg         `json:"error,omitempty"`
}

type errMsg struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Method: "bank.tx.arrived"

// BankTxArrivedParams represents a bank tx arrived params.
type BankTxArrivedParams struct {
	// Code is the code value for the bank tx arrived params.
	Code string `json:"code"`
}

// BankTxArrivedPayload represents a bank tx arrived payload.
type BankTxArrivedPayload struct {
	// Arrived is the arrived value for the bank tx arrived payload.
	Arrived bool `json:"arrived"`
}
