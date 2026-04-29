package hummingbot

import "encoding/json"

// PortfolioStateRequest is the body for POST /portfolio/state.
type PortfolioStateRequest struct {
	// AccountNames is the count associated with the portfolio state request.
	AccountNames []string `json:"account_names,omitempty"`
	// ConnectorNames is the connector names value for the portfolio state request.
	ConnectorNames []string `json:"connector_names,omitempty"`
	// SkipGateway is the skip gateway value for the portfolio state request.
	SkipGateway bool `json:"skip_gateway"`
	// Refresh is the refresh value for the portfolio state request.
	Refresh bool `json:"refresh"`
}

// BalanceToken is a single token entry within a connector's balance list.
// The Backend API returns: account_name -> connector_name -> []BalanceToken
// Field names are inferred from the Hummingbot Backend API source; they will
// be validated against live data once exchange credentials are configured.
type BalanceToken struct {
	// Token is the token value for the balance token.
	Token string `json:"token"`
	// Available is the available value for the balance token.
	Available float64 `json:"available"`
	// Total is the total value for the balance token.
	Total float64 `json:"total"`
	// USDValue is the usdvalue value for the balance token.
	USDValue float64 `json:"usd_value"`
}

// PortfolioState is the decoded form of the POST /portfolio/state response.
// Structure: account_name -> connector_name -> []BalanceToken
type PortfolioState map[string]map[string][]BalanceToken

// TradeFilterRequest is the body for POST /trading/trades.
type TradeFilterRequest struct {
	// Limit is the limit value for the trade filter request.
	Limit int `json:"limit,omitempty"`
	// AccountNames is the count associated with the trade filter request.
	AccountNames []string `json:"account_names,omitempty"`
	// ConnectorNames is the connector names value for the trade filter request.
	ConnectorNames []string `json:"connector_names,omitempty"`
	// TradingPairs is the trading pairs value for the trade filter request.
	TradingPairs []string `json:"trading_pairs,omitempty"`
	// TradeTypes is the trade types value for the trade filter request.
	TradeTypes []string `json:"trade_types,omitempty"` // "BUY" / "SELL"
}

// TradeRecord is a single trade entry from the paginated trades response.
type TradeRecord struct {
	// TradeID is the trade id value for the trade record.
	TradeID string `json:"trade_id"`
	// Connector is the connector value for the trade record.
	Connector string `json:"connector_name"`
	// TradingPair is the trading pair value for the trade record.
	TradingPair string `json:"trading_pair"`
	// TradeType is the trade type value for the trade record.
	TradeType string `json:"trade_type"` // "BUY" or "SELL"
	// Price is the price value used by the trade record.
	Price float64 `json:"price"`
	// Amount is the amount value for the trade record.
	Amount float64 `json:"amount"`
	// Fee is the fee value reported for the trade record.
	Fee float64 `json:"trade_fee"`
	// FeeCurrency names the asset or network for the trade record.
	FeeCurrency string `json:"fee_asset"`
	// Timestamp records the relevant time for the trade record.
	Timestamp int64 `json:"timestamp"` // unix ms
	// Raw preserves additional source data for the trade record.
	Raw json.RawMessage `json:"-"` // full response for inspection
}

// PaginatedResponse is the envelope returned by paginated endpoints.
type PaginatedResponse struct {
	// Data is the data value for the paginated response.
	Data []json.RawMessage `json:"data"`
	// Pagination is the pagination value for the paginated response.
	Pagination map[string]interface{} `json:"pagination"`
}

// StatusResponse is returned by GET /
type StatusResponse struct {
	// Name is the name value for the status response.
	Name string `json:"name"`
	// Version is the version value for the status response.
	Version string `json:"version"`
	// Status is the current status of the status response.
	Status string `json:"status"`
}
