# `internal/exchanges/dex/hodlhodl/models.go`

Source package: `hodlhodl`  
Source lines: `217`

## Package Purpose

This file contributes to the `hodlhodl` package.

## File Role

This file defines 12 type(s), declares 8 package value(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.

## Types

### `ContractStatus`

ContractStatus represents the lifecycle state of a HodlHodl contract.

Definition: `string`

### `Offer`

Offer is the JSON shape returned by GET /api/v1/offers/:id.

Fields:

- `ID` `string`: ID identifies the offer.
- `Version` `string`: Version is the version value for the offer.
- `Status` `string`: Status is the current status of the offer.
- `Side` `string`: Side is the side value for the offer.
- `Title` `string`: Title is the title value for the offer.
- `Description` `string`: Description is the description value for the offer.
- `CurrencyCode` `string`: CurrencyCode names the asset or network for the offer.
- `CountryCode` `string`: CountryCode is the count associated with the offer.
- `AssetCode` `string`: AssetCode is the asset code value for the offer.
- `MaxAmount` `string`: MaxAmount is the max amount value for the offer.
- `MinAmount` `string`: MinAmount is the min amount value for the offer.
- `Price` `string`: Price is the price value used by the offer.
- `FeePercentage` `string`: FeePercentage is a percentage value.
- `Trader` `Trader`: Trader is the trader value for the offer.
- `PaymentMethodInstructions` `[]PaymentMethodInstruction`: PaymentMethodInstructions is populated for "sell" offers (seller's payment details).
- `PaymentMethods` `[]json.RawMessage`: PaymentMethods is populated for "buy" offers (accepted payment methods).

### `Trader`

Trader is the nested trader info inside an Offer.

Fields:

- `Login` `string`: Login is the login value for the trader.
- `TradesCount` `int`: TradesCount is the count associated with the trader.
- `Rating` `float64`: Rating is the rating value for the trader.
- `VolumeTradedBTC` `string`: VolumeTradedBTC is a BTC-denominated volume traded btc value.
- `OnlineStatus` `string`: OnlineStatus is the current status of the trader.
- `LastOnlineAt` `string`: LastOnlineAt records the relevant time for the trader.
- `StrongHodler` `bool`: StrongHodler is the strong hodler value for the trader.
- `CountryCode` `string`: CountryCode is the count associated with the trader.
- `AveragePaymentTime` `int`: AveragePaymentTime records the relevant time for the trader.
- `AverageReleaseTime` `int`: AverageReleaseTime records the relevant time for the trader.

### `Contract`

Contract is the JSON shape returned by GET /api/v1/contracts/:id
and GET /api/v1/contracts/my.

Fields:

- `ID` `string`: ID identifies the contract.
- `Status` `ContractStatus`: Status is the current status of the contract.
- `YourRole` `string`: YourRole is the your role value for the contract.
- `OfferID` `string`: OfferID is the offer id value for the contract.
- `Price` `string`: Price is the price value used by the contract.
- `Value` `string`: Value is the value value for the contract.
- `Volume` `string`: Volume is the volume value for the contract.
- `CurrencyCode` `string`: CurrencyCode names the asset or network for the contract.
- `AssetCode` `string`: AssetCode is the asset code value for the contract.
- `Confirmations` `int`: Confirmations is the confirmations value for the contract.
- `ReleaseAddress` `string`: ReleaseAddress is the wallet or peer address used by the contract.
- `Escrow` `struct { Address string `json:"address"` Confirmations int `json:"confirmations"` AmountDeposited string `json:"amount_deposited"` AmountReleased string `json:"amount_released"` DepositTransactionID string `json:"deposit_transaction_id"` ReleaseTransactionID string `json:"release_transaction_id"` }`: Escrow is the escrow value for the contract.
- `Counterparty` `struct { Login string `json:"login"` OnlineStatus string `json:"online_status"` Rating string `json:"rating"` TradesCount int `json:"trades_count"` }`: Counterparty is the count associated with the contract.
- `CreatedAt` `string`: CreatedAt records the relevant time for the contract.

### `OfferSearchFilters`

OfferSearchFilters configures the GET /api/v1/offers query.

Fields:

- `Side` `string`: Side is the side value for the offer search filters.
- `CurrencyCode` `string`: CurrencyCode names the asset or network for the offer search filters.
- `AssetCode` `string`: AssetCode is the asset code value for the offer search filters.
- `Country` `string`: Country is the count associated with the offer search filters.
- `Amount` `string`: Amount is the amount value for the offer search filters.
- `Limit` `int`: Limit is the limit value for the offer search filters.
- `Offset` `int`: Offset is the offset value for the offer search filters.

### `CreateContractParams`

CreateContractParams are the required fields for POST /api/v1/contracts.

Fields:

- `OfferID` `string`: OfferID is the offer id value for the create contract params.
- `OfferVersion` `string`: OfferVersion is the offer version value for the create contract params.
- `PaymentMethodInstructionID` `string`: PaymentMethodInstructionID is the payment method instruction id value for the create contract params.
- `PaymentMethodInstructionVer` `string`: PaymentMethodInstructionVer is the payment method instruction ver value for the create contract params.
- `Value` `string`: Value is the value value for the create contract params.
- `Volume` `string`: Volume is the volume value for the create contract params.
- `Comment` `string`: Comment is the comment value for the create contract params.

### `PaymentMethodInstruction`

PaymentMethodInstruction represents a saved payment method on the user's
account (GET /api/v1/payment_method_instructions) and also appears nested
inside offers.

Fields:

- `ID` `string`: ID identifies the payment method instruction.
- `Version` `string`: Version is the version value for the payment method instruction.
- `PaymentMethodID` `string`: PaymentMethodID is the payment method id value for the payment method instruction.
- `Type` `string`: Type is the type value for the payment method instruction.
- `Name` `string`: Name is the name value for the payment method instruction.
- `Details` `string`: Details is the details value for the payment method instruction.

### `paymentMethodInstructionsResponse`

paymentMethodInstructionsResponse wraps the list returned by
GET /api/v1/payment_method_instructions.

Fields:

- `PaymentMethodInstructions` `[]PaymentMethodInstruction`: Stores the payment method instructions value for the payment method instructions response.

### `offerResponse`

offerResponse wraps a single offer.

Fields:

- `Offer` `Offer`: Stores the offer value for the offer response.

### `offersResponse`

offersResponse wraps a list of offers.

Fields:

- `Offers` `[]Offer`: Stores the offers value for the offers response.

### `contractResponse`

contractResponse wraps a single contract.

Fields:

- `Contract` `Contract`: Stores the contract value for the contract response.

### `contractsResponse`

contractsResponse wraps a list of contracts.

Fields:

- `Contracts` `[]Contract`: Stores the contracts value for the contracts response.

## Constants and Variables

- `ContractPending`: ContractPending is the contract pending value.
- `ContractDepositing`: ContractDepositing is the contract depositing value.
- `ContractInProgress`: ContractInProgress is the contract in progress value.
- `ContractPaid`: ContractPaid is the contract paid value.
- `ContractCompleted`: ContractCompleted is the contract completed value.
- `ContractCanceled`: ContractCanceled is the contract canceled value.
- `ContractDisputed`: ContractDisputed is the contract disputed value.
- `ContractResolved`: ContractResolved is the contract resolved value.

## Key Dependencies

- `encoding/json`

