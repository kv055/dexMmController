# `internal/exchanges/cex/kraken/client.go`

Source package: `kraken`  
Source lines: `392`

## Package Purpose

This file contributes to the `kraken` package.

## File Role

This file defines 13 type(s), implements 12 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Client`

Client represents a client.

Fields:

- `baseURL` `string`: Stores an endpoint or address used by the client.
- `apiKey` `string`: Stores the api key value for the client.
- `secretKey` `string`: Stores the secret key value for the client.
- `httpClient` `*http.Client`: Stores the http client value for the client.

### `DepositMethod`

DepositMethod represents a deposit method.

Fields:

- `Method` `string`: Method is the method value for the deposit method.
- `Limit` `bool`: Limit is the limit value for the deposit method.
- `GenAddress` `bool`: GenAddress is the wallet or peer address used by the deposit method.

### `DepositAddress`

DepositAddress represents a deposit address.

Fields:

- `Address` `string`: Address is the wallet or peer address used by the deposit address.
- `ExpireTM` `string`: ExpireTM is the expire tm value for the deposit address.
- `New` `bool`: New is the new value for the deposit address.

### `DepositAddressRequest`

DepositAddressRequest represents a deposit address request.

Fields:

- `Asset` `string`: Asset is the asset value for the deposit address request.
- `Method` `string`: Method is the method value for the deposit address request.
- `CreateNew` `bool`: CreateNew is the create new value for the deposit address request.
- `Amount` `string`: Amount is the amount value for the deposit address request.

### `WithdrawRequest`

WithdrawRequest represents a withdraw request.

Fields:

- `Asset` `string`: Asset is the asset value for the withdraw request.
- `Key` `string`: Key is the key value for the withdraw request.
- `Address` `string`: Address is the wallet or peer address used by the withdraw request.
- `Method` `string`: Method is the method value for the withdraw request.
- `Amount` `string`: Amount is the amount value for the withdraw request.

### `WithdrawMethod`

WithdrawMethod represents a withdraw method.

Fields:

- `Method` `string`: Method is the method value for the withdraw method.
- `Limit` `bool`: Limit is the limit value for the withdraw method.
- `Fee` `string`: Fee is the fee value reported for the withdraw method.

### `WithdrawResponse`

WithdrawResponse represents a withdraw response.

Fields:

- `RefID` `string`: RefID is the ref id value for the withdraw response.

### `DepositStatusRequest`

DepositStatusRequest represents a deposit status request.

Fields:

- `Asset` `string`: Asset is the asset value for the deposit status request.
- `Method` `string`: Method is the method value for the deposit status request.
- `StartTime` `*time.Time`: StartTime records the relevant time for the deposit status request.
- `EndTime` `*time.Time`: EndTime records the relevant time for the deposit status request.

### `DepositStatusRecord`

DepositStatusRecord represents a deposit status record.

Fields:

- `Method` `string`: Method is the method value for the deposit status record.
- `Asset` `string`: Asset is the asset value for the deposit status record.
- `RefID` `string`: RefID is the ref id value for the deposit status record.
- `TxID` `string`: TxID is the tx id value for the deposit status record.
- `Info` `string`: Info is the info value for the deposit status record.
- `Amount` `string`: Amount is the amount value for the deposit status record.
- `Fee` `string`: Fee is the fee value reported for the deposit status record.
- `Time` `int64`: Time records the relevant time for the deposit status record.
- `Status` `string`: Status is the current status of the deposit status record.

### `WithdrawStatusRequest`

WithdrawStatusRequest represents a withdraw status request.

Fields:

- `Asset` `string`: Asset is the asset value for the withdraw status request.
- `Method` `string`: Method is the method value for the withdraw status request.
- `StartTime` `*time.Time`: StartTime records the relevant time for the withdraw status request.
- `EndTime` `*time.Time`: EndTime records the relevant time for the withdraw status request.

### `WithdrawStatusRecord`

WithdrawStatusRecord represents a withdraw status record.

Fields:

- `Method` `string`: Method is the method value for the withdraw status record.
- `Asset` `string`: Asset is the asset value for the withdraw status record.
- `RefID` `string`: RefID is the ref id value for the withdraw status record.
- `TxID` `string`: TxID is the tx id value for the withdraw status record.
- `Info` `string`: Info is the info value for the withdraw status record.
- `Amount` `string`: Amount is the amount value for the withdraw status record.
- `Fee` `string`: Fee is the fee value reported for the withdraw status record.
- `Time` `int64`: Time records the relevant time for the withdraw status record.
- `Status` `string`: Status is the current status of the withdraw status record.

### `Balance`

Balance identifies a balance.

Definition: `map[string]string`

### `apiEnvelope`

Fields:

- `Error` `[]string`: Stores the error value for the api envelope.
- `Result` `T`: Stores the result value for the api envelope.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(baseURL, apiKey, secretKey string) *Client`

### `(*Client).Name`

Name returns the exchange or bridge name.

Signature: `func (c *Client) Name() string`

### `(*Client).Balance`

Balance retrieves balances from the exchange.

Signature: `func (c *Client) Balance(ctx context.Context) (Balance, error)`

### `(*Client).GetDepositMethods`

GetDepositMethods retrieves a deposit methods.

Signature: `func (c *Client) GetDepositMethods(ctx context.Context, asset string) ([]DepositMethod, error)`

### `(*Client).GetDepositAddresses`

GetDepositAddresses retrieves a deposit addresses.

Signature: `func (c *Client) GetDepositAddresses(ctx context.Context, asset, method string, createNew bool) ([]DepositAddress, error)`

### `(*Client).GetDepositAddressesWithOptions`

GetDepositAddressesWithOptions retrieves a deposit addresses with options.

Signature: `func (c *Client) GetDepositAddressesWithOptions(ctx context.Context, req DepositAddressRequest) ([]DepositAddress, error)`

### `(*Client).GetWithdrawMethods`

GetWithdrawMethods retrieves a withdraw methods.

Signature: `func (c *Client) GetWithdrawMethods(ctx context.Context, asset string) ([]WithdrawMethod, error)`

### `(*Client).Withdraw`

Withdraw withdraws funds using the configured bridge.

Signature: `func (c *Client) Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error)`

### `(*Client).GetDepositStatus`

GetDepositStatus retrieves a deposit status.

Signature: `func (c *Client) GetDepositStatus(ctx context.Context, req DepositStatusRequest) ([]DepositStatusRecord, error)`

### `(*Client).GetWithdrawStatus`

GetWithdrawStatus retrieves a withdraw status.

Signature: `func (c *Client) GetWithdrawStatus(ctx context.Context, req WithdrawStatusRequest) ([]WithdrawStatusRecord, error)`

### `(*Client).privateRequest`

This function is part of the `kraken` package implementation.

Signature: `func (c *Client) privateRequest(ctx context.Context, path string, form url.Values, out any) error`

### `sign`

This function is part of the `kraken` package implementation.

Signature: `func sign(path, nonce, encodedForm, secret string) (string, error)`

## Constants and Variables

- `defaultBaseURL`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `crypto/hmac`
- `crypto/sha256`
- `crypto/sha512`
- `encoding/base64`
- `encoding/json`
- `fmt`
- `io`
- `net/http`
- `net/url`
- `strconv`
- `strings`
- `time`

