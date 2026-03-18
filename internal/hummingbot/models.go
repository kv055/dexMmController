package hummingbot

import "encoding/json"

// PortfolioStateRequest is the body for POST /portfolio/state.
type PortfolioStateRequest struct {
	AccountNames   []string `json:"account_names,omitempty"`
	ConnectorNames []string `json:"connector_names,omitempty"`
	SkipGateway    bool     `json:"skip_gateway"`
	Refresh        bool     `json:"refresh"`
}

// BalanceToken is a single token entry within a connector's balance list.
// The Backend API returns: account_name -> connector_name -> []BalanceToken
// Field names are inferred from the Hummingbot Backend API source; they will
// be validated against live data once exchange credentials are configured.
type BalanceToken struct {
	Token     string  `json:"token"`
	Available float64 `json:"available"`
	Total     float64 `json:"total"`
	USDValue  float64 `json:"usd_value"`
}

// PortfolioState is the decoded form of the POST /portfolio/state response.
// Structure: account_name -> connector_name -> []BalanceToken
type PortfolioState map[string]map[string][]BalanceToken

// TradeFilterRequest is the body for POST /trading/trades.
type TradeFilterRequest struct {
	Limit          int      `json:"limit,omitempty"`
	AccountNames   []string `json:"account_names,omitempty"`
	ConnectorNames []string `json:"connector_names,omitempty"`
	TradingPairs   []string `json:"trading_pairs,omitempty"`
	TradeTypes     []string `json:"trade_types,omitempty"`    // "BUY" / "SELL"
}

// TradeRecord is a single trade entry from the paginated trades response.
type TradeRecord struct {
	TradeID     string          `json:"trade_id"`
	Connector   string          `json:"connector_name"`
	TradingPair string          `json:"trading_pair"`
	TradeType   string          `json:"trade_type"` // "BUY" or "SELL"
	Price       float64         `json:"price"`
	Amount      float64         `json:"amount"`
	Fee         float64         `json:"trade_fee"`
	FeeCurrency string          `json:"fee_asset"`
	Timestamp   int64           `json:"timestamp"` // unix ms
	Raw         json.RawMessage `json:"-"`          // full response for inspection
}

// PaginatedResponse is the envelope returned by paginated endpoints.
type PaginatedResponse struct {
	Data       []json.RawMessage      `json:"data"`
	Pagination map[string]interface{} `json:"pagination"`
}

// StatusResponse is returned by GET /
type StatusResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}
