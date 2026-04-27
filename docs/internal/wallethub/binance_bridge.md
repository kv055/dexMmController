# `internal/wallethub/binance_bridge.go`

Source package: `lnd`  
Source lines: `104`

## Package Purpose

This file contributes to the `lnd` package.

## File Role

This file defines 1 type(s), implements 6 function or method entry point(s).

## Runtime Behavior

- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `BinanceBridge`

BinanceBridge represents a binance bridge.

Fields:

- `client` `*binance.Client`: Stores the client value for the binance bridge.

## Functions and Methods

### `NewBinanceBridge`

NewBinanceBridge creates a binance bridge.

Signature: `func NewBinanceBridge(client *binance.Client) *BinanceBridge`

### `(*BinanceBridge).Name`

Name returns the exchange or bridge name.

Signature: `func (b *BinanceBridge) Name() string`

### `(*BinanceBridge).GetDepositAddress`

GetDepositAddress retrieves a deposit address.

Signature: `func (b *BinanceBridge) GetDepositAddress(ctx context.Context, coin, network string) (string, error)`

### `(*BinanceBridge).WithdrawToAddress`

WithdrawToAddress withdraws funds using the configured bridge.

Signature: `func (b *BinanceBridge) WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error)`

### `(*BinanceBridge).GetDepositHistory`

GetDepositHistory retrieves a deposit history.

Signature: `func (b *BinanceBridge) GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error)`

### `(*BinanceBridge).GetWithdrawHistory`

GetWithdrawHistory retrieves a withdraw history.

Signature: `func (b *BinanceBridge) GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error)`

## Key Dependencies

- `context`
- `dexMmController/internal/exchanges/cex/binance`

