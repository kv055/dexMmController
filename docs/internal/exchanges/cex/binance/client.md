# `internal/exchanges/cex/binance/client.go`

Source package: `binance`  
Source lines: `379`

## Package Purpose

This file contributes to the `binance` package.

## File Role

This file defines 9 type(s), implements 9 function or method entry point(s), declares 1 package value(s).

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

### `DepositAddressResponse`

DepositAddressResponse represents a deposit address response.

Fields:

- `Address` `string`: Address is the wallet or peer address used by the deposit address response.
- `Coin` `string`: Coin names the asset or network for the deposit address response.
- `Tag` `string`: Tag is the tag value for the deposit address response.
- `URL` `string`: URL is the endpoint URL.
- `IsDefault` `int`: IsDefault reports whether the deposit address response condition is true.

### `WithdrawRequest`

WithdrawRequest represents a withdraw request.

Fields:

- `Coin` `string`: Coin names the asset or network for the withdraw request.
- `Address` `string`: Address is the wallet or peer address used by the withdraw request.
- `Amount` `string`: Amount is the amount value for the withdraw request.
- `Network` `string`: Network names the asset or network for the withdraw request.
- `AddressTag` `string`: AddressTag is the wallet or peer address used by the withdraw request.
- `WithdrawOrderID` `string`: WithdrawOrderID preserves additional source data for the withdraw request.
- `TransactionFeeFlag` `*bool`: TransactionFeeFlag is the fee value reported for the withdraw request.
- `Name` `string`: Name is the name value for the withdraw request.
- `WalletType` `*int`: WalletType is the wallet type value for the withdraw request.
- `RecvWindow` `int64`: RecvWindow is the recv window value for the withdraw request.

### `WithdrawResponse`

WithdrawResponse represents a withdraw response.

Fields:

- `ID` `string`: ID identifies the withdraw response.

### `DepositHistoryParams`

DepositHistoryParams represents a deposit history params.

Fields:

- `Coin` `string`: Coin names the asset or network for the deposit history params.
- `Status` `*int`: Status is the current status of the deposit history params.
- `StartTime` `*time.Time`: StartTime records the relevant time for the deposit history params.
- `EndTime` `*time.Time`: EndTime records the relevant time for the deposit history params.
- `Offset` `*int`: Offset is the offset value for the deposit history params.
- `Limit` `*int`: Limit is the limit value for the deposit history params.
- `RecvWindow` `int64`: RecvWindow is the recv window value for the deposit history params.

### `DepositRecord`

DepositRecord represents a deposit record.

Fields:

- `ID` `string`: ID identifies the deposit record.
- `Amount` `string`: Amount is the amount value for the deposit record.
- `Coin` `string`: Coin names the asset or network for the deposit record.
- `Network` `string`: Network names the asset or network for the deposit record.
- `Status` `int`: Status is the current status of the deposit record.
- `Address` `string`: Address is the wallet or peer address used by the deposit record.
- `AddressTag` `string`: AddressTag is the wallet or peer address used by the deposit record.
- `TxID` `string`: TxID is the tx id value for the deposit record.
- `InsertTime` `int64`: InsertTime records the relevant time for the deposit record.
- `TransferType` `int`: TransferType is the transfer type value for the deposit record.
- `ConfirmTimes` `string`: ConfirmTimes records the relevant time for the deposit record.
- `UnlockConfirm` `string`: UnlockConfirm is the unlock confirm value for the deposit record.

### `WithdrawHistoryParams`

WithdrawHistoryParams represents a withdraw history params.

Fields:

- `Coin` `string`: Coin names the asset or network for the withdraw history params.
- `WithdrawOrderID` `string`: WithdrawOrderID preserves additional source data for the withdraw history params.
- `Status` `*int`: Status is the current status of the withdraw history params.
- `StartTime` `*time.Time`: StartTime records the relevant time for the withdraw history params.
- `EndTime` `*time.Time`: EndTime records the relevant time for the withdraw history params.
- `Offset` `*int`: Offset is the offset value for the withdraw history params.
- `Limit` `*int`: Limit is the limit value for the withdraw history params.
- `RecvWindow` `int64`: RecvWindow is the recv window value for the withdraw history params.

### `WithdrawRecord`

WithdrawRecord represents a withdraw record.

Fields:

- `ID` `string`: ID identifies the withdraw record.
- `Amount` `string`: Amount is the amount value for the withdraw record.
- `TransactionFee` `string`: TransactionFee is the fee value reported for the withdraw record.
- `Coin` `string`: Coin names the asset or network for the withdraw record.
- `Status` `int`: Status is the current status of the withdraw record.
- `Address` `string`: Address is the wallet or peer address used by the withdraw record.
- `ApplyTime` `string`: ApplyTime records the relevant time for the withdraw record.
- `Network` `string`: Network names the asset or network for the withdraw record.
- `TransferType` `int`: TransferType is the transfer type value for the withdraw record.
- `WithdrawOrderID` `string`: WithdrawOrderID preserves additional source data for the withdraw record.
- `TxID` `string`: TxID is the tx id value for the withdraw record.

### `AccountInfo`

AccountInfo reports Binance account permissions and balances.

Fields:

- `CanTrade` `bool`: CanTrade is the can trade value for the account info.
- `CanWithdraw` `bool`: CanWithdraw preserves additional source data for the account info.
- `CanDeposit` `bool`: CanDeposit is the can deposit value for the account info.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(baseURL, apiKey, secretKey string) *Client`

### `(*Client).Name`

Name returns the exchange or bridge name.

Signature: `func (c *Client) Name() string`

### `(*Client).AccountInfo`

AccountInfo retrieves account capability and permission details.

Signature: `func (c *Client) AccountInfo(ctx context.Context) (*AccountInfo, error)`

### `(*Client).GetDepositAddress`

GetDepositAddress retrieves a deposit address.

Signature: `func (c *Client) GetDepositAddress(ctx context.Context, coin, network string) (*DepositAddressResponse, error)`

### `(*Client).ApplyWithdraw`

ApplyWithdraw submits a withdrawal request to Binance.

Signature: `func (c *Client) ApplyWithdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error)`

### `(*Client).GetDepositHistory`

GetDepositHistory retrieves a deposit history.

Signature: `func (c *Client) GetDepositHistory(ctx context.Context, params DepositHistoryParams) ([]DepositRecord, error)`

### `(*Client).GetWithdrawHistory`

GetWithdrawHistory retrieves a withdraw history.

Signature: `func (c *Client) GetWithdrawHistory(ctx context.Context, params WithdrawHistoryParams) ([]WithdrawRecord, error)`

### `(*Client).signedRequest`

This function is part of the `binance` package implementation.

Signature: `func (c *Client) signedRequest(ctx context.Context, method, path string, params url.Values, headers http.Header, out any) error`

### `sign`

This function is part of the `binance` package implementation.

Signature: `func sign(payload, secret string) string`

## Constants and Variables

- `defaultBaseURL`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `crypto/hmac`
- `crypto/sha256`
- `encoding/hex`
- `encoding/json`
- `fmt`
- `io`
- `net/http`
- `net/url`
- `strconv`
- `strings`
- `time`

