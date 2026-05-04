# `internal/db/models.go`

Source package: `db`  
Source lines: `220`

## Package Purpose

This file contributes to the `db` package.

## File Role

This file defines 6 type(s), implements 3 function or method entry point(s), declares 20 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.

## Types

### `Account`

Account is a shared provider/account row owned by dexUnifiedApi schema files.

Fields:

- `ID` `uint`: ID identifies the account.
- `Provider` `string`: Provider is the provider value for the account.
- `Email` `string`: Email is the email value for the account.
- `Password` `string`: Password is the password value for the account.
- `Phone` `string`: Phone is the phone value for the account.
- `LastHealthCheck` `time.Time`: LastHealthCheck is the last health check value for the account.
- `IsHealthy` `bool`: IsHealthy reports whether the account condition is true.
- `CreatedAt` `time.Time`: CreatedAt records the relevant time for the account.
- `UpdatedAt` `time.Time`: UpdatedAt records the relevant time for the account.

### `APIKey`

APIKey is a shared account API-key row owned by dexUnifiedApi schema files.

Fields:

- `ID` `uint`: ID identifies the apikey.
- `AccountID` `uint`: AccountID is the count associated with the apikey.
- `Name` `string`: Name is the name value for the apikey.
- `APIKey` `string`: APIKey is the apikey value for the apikey.
- `APISecret` `string`: APISecret is the apisecret value for the apikey.
- `Passphrase` `string`: Passphrase is the passphrase value for the apikey.
- `IsActive` `bool`: IsActive reports whether the apikey condition is true.
- `CreatedAt` `time.Time`: CreatedAt records the relevant time for the apikey.
- `UpdatedAt` `time.Time`: UpdatedAt records the relevant time for the apikey.

### `CexExchange`

CexExchange represents a supported centralized exchange

Definition: `string`

### `DexPlatform`

DexPlatform represents a supported decentralized exchange

Definition: `string`

### `TransactionStatus`

TransactionStatus represents the lifecycle state of a transaction

Definition: `string`

### `Transaction`

Transaction represents a crypto trading transaction record

Fields:

- `ID` `uint`: ID identifies the transaction.
- `Direction` `string`: Direction is the direction value for the transaction.
- `FiatAmount` `float64`: FiatAmount is a fiat-denominated fiat amount value.
- `FiatCurrency` `string`: FiatCurrency is a fiat-denominated fiat currency value.
- `Cex` `CexExchange`: Cex is the cex value for the transaction.
- `CexHedgingPosition` `CexExchange`: CexHedgingPosition is the cex hedging position value for the transaction.
- `Dex` `DexPlatform`: Dex is the dex value for the transaction.
- `Status` `TransactionStatus`: Status is the current status of the transaction.
- `CexTransactionDetails` `json.RawMessage`: CexTransactionDetails is the cex transaction details value for the transaction.
- `CexBitcoinAmount` `float64`: CexBitcoinAmount is the cex bitcoin amount value for the transaction.
- `CexTransactionTimestamp` `time.Time`: CexTransactionTimestamp records the relevant time for the transaction.
- `CexTransactionFee` `float64`: CexTransactionFee is the fee value reported for the transaction.
- `CexToDexWithdrawalTxDetails` `json.RawMessage`: CexToDexWithdrawalTxDetails preserves additional source data for the transaction.
- `CexWithdrawalFee` `float64`: CexWithdrawalFee is the fee value reported for the transaction.
- `DexBtcAddress` `string`: DexBtcAddress is a BTC-denominated dex btc address value.
- `CexCounterPositionOpenDetails` `json.RawMessage`: CexCounterPositionOpenDetails is the count associated with the transaction.
- `CexCounterPositionCloseDetails` `json.RawMessage`: CexCounterPositionCloseDetails is the count associated with the transaction.
- `CexCounterPositionFee` `float64`: CexCounterPositionFee is the count associated with the transaction.
- `DexOfferCreation` `json.RawMessage`: DexOfferCreation is the dex offer creation value for the transaction.
- `DexOfferTaken` `time.Time`: DexOfferTaken is the dex offer taken value for the transaction.
- `DexFiatArrival` `json.RawMessage`: DexFiatArrival is a fiat-denominated dex fiat arrival value.
- `DexBtcRelease` `time.Time`: DexBtcRelease is a BTC-denominated dex btc release value.
- `DexFees` `float64`: DexFees is the fee value reported for the transaction.
- `TransferFiatToCexStartTime` `time.Time`: TransferFiatToCexStartTime is a fiat-denominated transfer fiat to cex start time value.
- `TransferFiatToCexArrivalTime` `time.Time`: TransferFiatToCexArrivalTime is a fiat-denominated transfer fiat to cex arrival time value.
- `TransactionComplete` `bool`: TransactionComplete is the transaction complete value for the transaction.
- `OpenClawLlmInferenceCost` `float64`: OpenClawLlmInferenceCost is the open claw llm inference cost value for the transaction.
- `VpsHostingCost` `float64`: VpsHostingCost is the vps hosting cost value for the transaction.
- `TotalTransactionPnl` `float64`: TotalTransactionPnl is the total transaction pnl value for the transaction.
- `TotalTransactionTimeElapsed` `int64`: TotalTransactionTimeElapsed records the relevant time for the transaction.
- `TotalTransactionVolatilityInBtcPrice` `float64`: TotalTransactionVolatilityInBtcPrice is a BTC-denominated total transaction volatility in btc price value.
- `PremiumOverCexPriceToBreakEvenPrediction` `float64`: PremiumOverCexPriceToBreakEvenPrediction is the price value used by the transaction.
- `CreatedAt` `time.Time`: CreatedAt records the relevant time for the transaction.
- `UpdatedAt` `time.Time`: UpdatedAt records the relevant time for the transaction.
- `DeletedAt` `gorm.DeletedAt`: DeletedAt records the relevant time for the transaction.

## Functions and Methods

### `(Account).TableName`

TableName returns the database table name for the model.

Signature: `func (Account) TableName() string`

### `(APIKey).TableName`

TableName returns the database table name for the model.

Signature: `func (APIKey) TableName() string`

### `(Transaction).TableName`

TableName specifies the table name for this model

Signature: `func (Transaction) TableName() string`

## Constants and Variables

- `CexBinance`: CexBinance is the cex binance value.
- `CexKraken`: CexKraken is the cex kraken value.
- `CexCoinbase`: CexCoinbase is the cex coinbase value.
- `CexBybit`: CexBybit is the cex bybit value.
- `CexOKX`: CexOKX is the cex okx value.
- `CexBitfinex`: CexBitfinex is the cex bitfinex value.
- `CexGemini`: CexGemini is the cex gemini value.
- `CexBitStamp`: CexBitStamp is the cex bit stamp value.
- `DexBisq`: DexBisq is the dex bisq value.
- `DexHodlHodl`: DexHodlHodl is the dex hodl hodl value.
- `DexRoboSats`: DexRoboSats is the dex robo sats value.
- `StatusFiatToCexInitiated`: StatusFiatToCexInitiated is the status fiat to cex initiated value.
- `StatusFiatToCexArrived`: StatusFiatToCexArrived is the status fiat to cex arrived value.
- `StatusCexTradeSuccessful`: StatusCexTradeSuccessful is the status cex trade successful value.
- `StatusCexCryptoWithdrawalInitiated`: StatusCexCryptoWithdrawalInitiated is the status cex crypto withdrawal initiated value.
- `StatusCexCryptoWithdrawalArrived`: StatusCexCryptoWithdrawalArrived is the status cex crypto withdrawal arrived value.
- `StatusDexPositionOpen`: StatusDexPositionOpen is the status dex position open value.
- `StatusDexPositionTaken`: StatusDexPositionTaken is the status dex position taken value.
- `StatusDexFiatHasArrived`: StatusDexFiatHasArrived is the status dex fiat has arrived value.
- `StatusDexPositionReleased`: StatusDexPositionReleased is the status dex position released value.

## Key Dependencies

- `encoding/json`
- `time`
- `gorm.io/gorm`
