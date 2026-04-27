# `internal/hummingbot/client.go`

Source package: `hummingbot`  
Source lines: `144`

## Package Purpose

This file contributes to the `hummingbot` package.

## File Role

This file defines 1 type(s), implements 9 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Client`

Client is a REST client for the Hummingbot Backend API (port 8000).
Auth is HTTP Basic on every request — no token exchange required.

Fields:

- `baseURL` `string`: Stores an endpoint or address used by the client.
- `username` `string`: Stores the username value for the client.
- `password` `string`: Stores the password value for the client.
- `http` `*http.Client`: Stores the http value for the client.

## Functions and Methods

### `NewClient`

NewClient creates a new Hummingbot Backend API client.
baseURL should be e.g. "http://localhost:8000".

Signature: `func NewClient(baseURL, username, password string) *Client`

### `(*Client).get`

get performs an authenticated GET and decodes the JSON response into out.

Signature: `func (c *Client) get(ctx context.Context, path string, out any) error`

### `(*Client).post`

post performs an authenticated POST with a JSON body and decodes the response into out.

Signature: `func (c *Client) post(ctx context.Context, path string, body any, out any) error`

### `(*Client).do`

This function is part of the `hummingbot` package implementation.

Signature: `func (c *Client) do(req *http.Request, path string, out any) error`

### `(*Client).Status`

Status returns the current running state of the Hummingbot API instance.

Signature: `func (c *Client) Status(ctx context.Context) (*StatusResponse, error)`

### `(*Client).PortfolioState`

PortfolioState returns the full portfolio state across all accounts and connectors.

Signature: `func (c *Client) PortfolioState(ctx context.Context, req PortfolioStateRequest) (PortfolioState, error)`

### `(*Client).BalanceForExchange`

BalanceForExchange returns the balance of a specific token on a specific connector.
connector is the Hummingbot connector name (e.g. "kraken", "binance").
token is the asset symbol (e.g. "USD", "BTC").

Signature: `func (c *Client) BalanceForExchange(ctx context.Context, connector, token string) (*BalanceToken, error)`

### `(*Client).Trades`

Trades returns a paginated list of trades, optionally filtered.

Signature: `func (c *Client) Trades(ctx context.Context, req TradeFilterRequest) ([]TradeRecord, error)`

### `(*Client).TradesForExchange`

TradesForExchange returns all trades for a specific connector/exchange.
connector is the Hummingbot connector name (e.g. "kraken").

Signature: `func (c *Client) TradesForExchange(ctx context.Context, connector string) ([]TradeRecord, error)`

## Key Dependencies

- `bytes`
- `context`
- `encoding/json`
- `fmt`
- `net/http`
- `time`

