# `internal/wallethub/kraken_bridge.go`

Source package: `lnd`  
Source lines: `217`

## Package Purpose

This file contributes to the `lnd` package.

## File Role

This file defines 1 type(s), implements 10 function or method entry point(s).

## Runtime Behavior

- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `KrakenBridge`

KrakenBridge represents a kraken bridge.

Fields:

- `client` `*kraken.Client`: Stores the client value for the kraken bridge.
- `depositMethodByCoin` `map[string]string`: Stores the deposit method by coin value for the kraken bridge.
- `withdrawKeyByCoin` `map[string]string`: Stores the withdraw key by coin value for the kraken bridge.
- `lightningDepositMethodByCoin` `map[string]string`: Stores the lightning deposit method by coin value for the kraken bridge.
- `lightningWithdrawMethodByCoin` `map[string]string`: Stores the lightning withdraw method by coin value for the kraken bridge.

## Functions and Methods

### `NewKrakenBridge`

NewKrakenBridge creates a kraken bridge.

Signature: `func NewKrakenBridge( client *kraken.Client, depositMethodByCoin map[string]string, withdrawKeyByCoin map[string]string, lightningDepositMethodByCoin map[string]string, lightningWithdrawMethodByCoin map[string]string, ) *KrakenBridge`

### `(*KrakenBridge).Name`

Name returns the exchange or bridge name.

Signature: `func (k *KrakenBridge) Name() string`

### `(*KrakenBridge).GetDepositAddress`

GetDepositAddress retrieves a deposit address.

Signature: `func (k *KrakenBridge) GetDepositAddress(ctx context.Context, coin, network string) (string, error)`

### `(*KrakenBridge).WithdrawToAddress`

WithdrawToAddress withdraws funds using the configured bridge.

Signature: `func (k *KrakenBridge) WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error)`

### `(*KrakenBridge).GetLightningDepositInvoice`

GetLightningDepositInvoice retrieves a lightning deposit invoice.

Signature: `func (k *KrakenBridge) GetLightningDepositInvoice(ctx context.Context, req ExchangeLightningDepositRequest) (string, error)`

### `(*KrakenBridge).WithdrawByInvoice`

WithdrawByInvoice withdraws funds using the configured bridge.

Signature: `func (k *KrakenBridge) WithdrawByInvoice(ctx context.Context, req ExchangeLightningWithdrawRequest) (*ExchangeLightningWithdrawResponse, error)`

### `(*KrakenBridge).GetDepositHistory`

GetDepositHistory retrieves a deposit history.

Signature: `func (k *KrakenBridge) GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error)`

### `(*KrakenBridge).GetWithdrawHistory`

GetWithdrawHistory retrieves a withdraw history.

Signature: `func (k *KrakenBridge) GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error)`

### `mapKrakenStatus`

This function is part of the `lnd` package implementation.

Signature: `func mapKrakenStatus(status string) int`

### `satsToBTCString`

This function is part of the `lnd` package implementation.

Signature: `func satsToBTCString(amountSat int64) string`

## Key Dependencies

- `context`
- `fmt`
- `strconv`
- `dexTakerController/internal/exchanges/cex/kraken`

