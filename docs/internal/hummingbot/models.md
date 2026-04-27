# `internal/hummingbot/models.go`

Source package: `hummingbot`  
Source lines: `91`

## Package Purpose

This file contributes to the `hummingbot` package.

## File Role

This file defines 7 type(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.

## Types

### `PortfolioStateRequest`

PortfolioStateRequest is the body for POST /portfolio/state.

Fields:

- `AccountNames` `[]string`: AccountNames is the count associated with the portfolio state request.
- `ConnectorNames` `[]string`: ConnectorNames is the connector names value for the portfolio state request.
- `SkipGateway` `bool`: SkipGateway is the skip gateway value for the portfolio state request.
- `Refresh` `bool`: Refresh is the refresh value for the portfolio state request.

### `BalanceToken`

BalanceToken is a single token entry within a connector's balance list.
The Backend API returns: account_name -> connector_name -> []BalanceToken
Field names are inferred from the Hummingbot Backend API source; they will
be validated against live data once exchange credentials are configured.

Fields:

- `Token` `string`: Token is the token value for the balance token.
- `Available` `float64`: Available is the available value for the balance token.
- `Total` `float64`: Total is the total value for the balance token.
- `USDValue` `float64`: USDValue is the usdvalue value for the balance token.

### `PortfolioState`

PortfolioState is the decoded form of the POST /portfolio/state response.
Structure: account_name -> connector_name -> []BalanceToken

Definition: `map[string]map[string][]BalanceToken`

### `TradeFilterRequest`

TradeFilterRequest is the body for POST /trading/trades.

Fields:

- `Limit` `int`: Limit is the limit value for the trade filter request.
- `AccountNames` `[]string`: AccountNames is the count associated with the trade filter request.
- `ConnectorNames` `[]string`: ConnectorNames is the connector names value for the trade filter request.
- `TradingPairs` `[]string`: TradingPairs is the trading pairs value for the trade filter request.
- `TradeTypes` `[]string`: TradeTypes is the trade types value for the trade filter request.

### `TradeRecord`

TradeRecord is a single trade entry from the paginated trades response.

Fields:

- `TradeID` `string`: TradeID is the trade id value for the trade record.
- `Connector` `string`: Connector is the connector value for the trade record.
- `TradingPair` `string`: TradingPair is the trading pair value for the trade record.
- `TradeType` `string`: TradeType is the trade type value for the trade record.
- `Price` `float64`: Price is the price value used by the trade record.
- `Amount` `float64`: Amount is the amount value for the trade record.
- `Fee` `float64`: Fee is the fee value reported for the trade record.
- `FeeCurrency` `string`: FeeCurrency names the asset or network for the trade record.
- `Timestamp` `int64`: Timestamp records the relevant time for the trade record.
- `Raw` `json.RawMessage`: Raw preserves additional source data for the trade record.

### `PaginatedResponse`

PaginatedResponse is the envelope returned by paginated endpoints.

Fields:

- `Data` `[]json.RawMessage`: Data is the data value for the paginated response.
- `Pagination` `map[string]interface{}`: Pagination is the pagination value for the paginated response.

### `StatusResponse`

StatusResponse is returned by GET /

Fields:

- `Name` `string`: Name is the name value for the status response.
- `Version` `string`: Version is the version value for the status response.
- `Status` `string`: Status is the current status of the status response.

## Key Dependencies

- `encoding/json`

