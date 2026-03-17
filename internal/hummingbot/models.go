package hummingbot

// AuthRequest is sent to POST /api/v1/auth
type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse is returned by POST /api/v1/auth
type AuthResponse struct {
	Token string `json:"token"`
}

// BalanceEntry holds the balance for a single asset on a single exchange.
type BalanceEntry struct {
	Exchange  string  `json:"exchange"`
	Asset     string  `json:"asset"`
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
}

// BalanceResponse is returned by GET /api/v1/balance
type BalanceResponse struct {
	Balances []BalanceEntry `json:"balances"`
}

// TradeRecord represents a single executed trade in the hummingbot history.
type TradeRecord struct {
	TradeID    string  `json:"trade_id"`
	Exchange   string  `json:"exchange"`
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"` // "BUY" or "SELL"
	Price      float64 `json:"price"`
	Amount     float64 `json:"amount"`
	Fee        float64 `json:"fee"`
	FeeCurrency string `json:"fee_currency"`
	Timestamp  int64   `json:"timestamp"` // unix ms
}

// TradeHistoryResponse is returned by GET /api/v1/history
type TradeHistoryResponse struct {
	Trades []TradeRecord `json:"trades"`
}

// WithdrawalRecord represents a single withdrawal entry.
type WithdrawalRecord struct {
	WithdrawalID string  `json:"id"`
	Exchange     string  `json:"exchange"`
	Asset        string  `json:"asset"`
	Amount       float64 `json:"amount"`
	Fee          float64 `json:"fee"`
	TxHash       string  `json:"tx_hash"`
	Status       string  `json:"status"` // "pending", "confirmed", "failed"
	Timestamp    int64   `json:"timestamp"` // unix ms
}

// WithdrawalHistoryResponse is returned by GET /api/v1/withdrawals
type WithdrawalHistoryResponse struct {
	Withdrawals []WithdrawalRecord `json:"withdrawals"`
}

// StatusResponse is returned by GET /api/v1/status
type StatusResponse struct {
	Running    bool   `json:"running"`
	Strategy   string `json:"strategy"`
	Message    string `json:"message"`
}
