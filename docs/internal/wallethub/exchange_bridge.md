# `internal/wallethub/exchange_bridge.go`

Source package: `lnd`  
Source lines: `363`

## Package Purpose

This file contributes to the `lnd` package.

## File Role

This file defines 16 type(s), implements 6 function or method entry point(s).

## Runtime Behavior

- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `ExchangeBridge`

ExchangeBridge defines the behavior required by this package.

Definition: `interface { Name() string GetDepositAddress(ctx context.Context, coin, network string) (string, error) WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error) GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error) GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error) }`

### `LightningExchangeBridge`

LightningExchangeBridge defines the behavior required by this package.

Definition: `interface { ExchangeBridge GetLightningDepositInvoice(ctx context.Context, req ExchangeLightningDepositRequest) (string, error) WithdrawByInvoice(ctx context.Context, req ExchangeLightningWithdrawRequest) (*ExchangeLightningWithdrawResponse, error) }`

### `ExchangeWithdrawRequest`

ExchangeWithdrawRequest contains the parameters for an on-chain exchange withdrawal.

Fields:

- `Coin` `string`: Coin names the asset or network for the exchange withdraw request.
- `Network` `string`: Network names the asset or network for the exchange withdraw request.
- `Address` `string`: Address is the wallet or peer address used by the exchange withdraw request.
- `Amount` `string`: Amount is the amount value for the exchange withdraw request.
- `AddressTag` `string`: AddressTag is the wallet or peer address used by the exchange withdraw request.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the exchange withdraw request.

### `ExchangeWithdrawResponse`

ExchangeWithdrawResponse contains the exchange withdrawal identifier.

Fields:

- `ID` `string`: ID identifies the exchange withdraw response.

### `ExchangeLightningDepositRequest`

ExchangeLightningDepositRequest contains the parameters for creating a Lightning deposit invoice.

Fields:

- `Coin` `string`: Coin names the asset or network for the exchange lightning deposit request.
- `Network` `string`: Network names the asset or network for the exchange lightning deposit request.
- `AmountSat` `int64`: AmountSat is the amount in satoshis.
- `Memo` `string`: Memo carries a human-readable message for the exchange lightning deposit request.

### `ExchangeLightningWithdrawRequest`

ExchangeLightningWithdrawRequest contains the parameters for paying a Lightning invoice from an exchange.

Fields:

- `Coin` `string`: Coin names the asset or network for the exchange lightning withdraw request.
- `Network` `string`: Network names the asset or network for the exchange lightning withdraw request.
- `Invoice` `string`: Invoice is the invoice value for the exchange lightning withdraw request.
- `Amount` `string`: Amount is the amount value for the exchange lightning withdraw request.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the exchange lightning withdraw request.

### `ExchangeLightningWithdrawResponse`

ExchangeLightningWithdrawResponse contains the exchange Lightning withdrawal identifier.

Fields:

- `ID` `string`: ID identifies the exchange lightning withdraw response.

### `ExchangeDepositHistoryParams`

ExchangeDepositHistoryParams filters exchange deposit history queries.

Fields:

- `Coin` `string`: Coin names the asset or network for the exchange deposit history params.
- `StartTime` `*time.Time`: StartTime records the relevant time for the exchange deposit history params.
- `EndTime` `*time.Time`: EndTime records the relevant time for the exchange deposit history params.

### `ExchangeWithdrawHistoryParams`

ExchangeWithdrawHistoryParams filters exchange withdrawal history queries.

Fields:

- `Coin` `string`: Coin names the asset or network for the exchange withdraw history params.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the exchange withdraw history params.
- `StartTime` `*time.Time`: StartTime records the relevant time for the exchange withdraw history params.
- `EndTime` `*time.Time`: EndTime records the relevant time for the exchange withdraw history params.

### `ExchangeDepositRecord`

ExchangeDepositRecord is a normalized deposit-history entry from an exchange.

Fields:

- `ID` `string`: ID identifies the exchange deposit record.
- `Coin` `string`: Coin names the asset or network for the exchange deposit record.
- `Network` `string`: Network names the asset or network for the exchange deposit record.
- `Address` `string`: Address is the wallet or peer address used by the exchange deposit record.
- `Amount` `string`: Amount is the amount value for the exchange deposit record.
- `Status` `int`: Status is the current status of the exchange deposit record.
- `TxID` `string`: TxID is the tx id value for the exchange deposit record.
- `InsertTime` `int64`: InsertTime records the relevant time for the exchange deposit record.

### `ExchangeWithdrawRecord`

ExchangeWithdrawRecord is a normalized withdrawal-history entry from an exchange.

Fields:

- `ID` `string`: ID identifies the exchange withdraw record.
- `Coin` `string`: Coin names the asset or network for the exchange withdraw record.
- `Network` `string`: Network names the asset or network for the exchange withdraw record.
- `Address` `string`: Address is the wallet or peer address used by the exchange withdraw record.
- `Amount` `string`: Amount is the amount value for the exchange withdraw record.
- `Status` `int`: Status is the current status of the exchange withdraw record.
- `TxID` `string`: TxID is the tx id value for the exchange withdraw record.
- `ApplyTime` `string`: ApplyTime records the relevant time for the exchange withdraw record.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the exchange withdraw record.

### `WalletExchangeBridge`

WalletExchangeBridge represents a wallet exchange bridge.

Fields:

- `lnd` `*Client`: Stores the lnd value for the wallet exchange bridge.
- `exchange` `ExchangeBridge`: Stores the exchange value for the wallet exchange bridge.

### `WithdrawFromExchangeRequest`

WithdrawFromExchangeRequest represents a withdraw from exchange request.

Fields:

- `Coin` `string`: Coin names the asset or network for the withdraw from exchange request.
- `Network` `string`: Network names the asset or network for the withdraw from exchange request.
- `Amount` `string`: Amount is the amount value for the withdraw from exchange request.
- `LNDDestination` `string`: LNDDestination is the lnddestination value for the withdraw from exchange request.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the withdraw from exchange request.
- `GenerateNewLNDAdress` `bool`: GenerateNewLNDAdress is the generate new lndadress value for the withdraw from exchange request.

### `WithdrawFromExchangeResult`

WithdrawFromExchangeResult represents a withdraw from exchange result.

Fields:

- `Exchange` `string`: Exchange is the exchange value for the withdraw from exchange result.
- `WithdrawalID` `string`: WithdrawalID preserves additional source data for the withdraw from exchange result.
- `WalletAddress` `string`: WalletAddress is the wallet or peer address used by the withdraw from exchange result.
- `ClientRequestID` `string`: ClientRequestID is the client request id value for the withdraw from exchange result.
- `Invoice` `string`: Invoice is the invoice value for the withdraw from exchange result.

### `DepositToExchangeRequest`

DepositToExchangeRequest represents a deposit to exchange request.

Fields:

- `Coin` `string`: Coin names the asset or network for the deposit to exchange request.
- `Network` `string`: Network names the asset or network for the deposit to exchange request.
- `AmountSat` `int64`: AmountSat is the amount in satoshis.
- `SatPerVByte` `int64`: SatPerVByte is the sat per vbyte value for the deposit to exchange request.

### `DepositToExchangeResult`

DepositToExchangeResult represents a deposit to exchange result.

Fields:

- `Exchange` `string`: Exchange is the exchange value for the deposit to exchange result.
- `DepositAddress` `string`: DepositAddress is the wallet or peer address used by the deposit to exchange result.
- `OnChainTxID` `string`: OnChainTxID is the on chain tx id value for the deposit to exchange result.
- `Invoice` `string`: Invoice is the invoice value for the deposit to exchange result.
- `PaymentPreimage` `string`: PaymentPreimage is the payment preimage value for the deposit to exchange result.

## Functions and Methods

### `NewWalletExchangeBridge`

NewWalletExchangeBridge creates a wallet exchange bridge.

Signature: `func NewWalletExchangeBridge(lndClient *Client, exchange ExchangeBridge) *WalletExchangeBridge`

### `(*WalletExchangeBridge).WithdrawFromExchangeToLND`

WithdrawFromExchangeToLND withdraws funds using the configured bridge.

Signature: `func (b *WalletExchangeBridge) WithdrawFromExchangeToLND(ctx context.Context, req WithdrawFromExchangeRequest) (*WithdrawFromExchangeResult, error)`

### `(*WalletExchangeBridge).DepositFromLNDToExchange`

DepositFromLNDToExchange deposits funds using the configured bridge.

Signature: `func (b *WalletExchangeBridge) DepositFromLNDToExchange(ctx context.Context, req DepositToExchangeRequest) (*DepositToExchangeResult, error)`

### `(*WalletExchangeBridge).WithdrawFromExchangeToLNDLightning`

WithdrawFromExchangeToLNDLightning withdraws funds using the configured bridge.

Signature: `func (b *WalletExchangeBridge) WithdrawFromExchangeToLNDLightning( ctx context.Context, req WithdrawFromExchangeRequest, ) (*WithdrawFromExchangeResult, error)`

### `(*WalletExchangeBridge).DepositFromLNDToExchangeLightning`

DepositFromLNDToExchangeLightning deposits funds using the configured bridge.

Signature: `func (b *WalletExchangeBridge) DepositFromLNDToExchangeLightning( ctx context.Context, req DepositToExchangeRequest, ) (*DepositToExchangeResult, error)`

### `parseAmountToSats`

This function is part of the `lnd` package implementation.

Signature: `func parseAmountToSats(amount string) (int64, error)`

## Key Dependencies

- `context`
- `fmt`
- `strconv`
- `time`

