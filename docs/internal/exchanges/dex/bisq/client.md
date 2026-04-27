# `internal/exchanges/dex/bisq/client.go`

Source package: `bisq`  
Source lines: `685`

## Package Purpose

This file contributes to the `bisq` package.

## File Role

This file defines 7 type(s), implements 33 function or method entry point(s), declares 7 package value(s).

## Runtime Behavior

- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates with a gRPC service.

## Types

### `Client`

Client represents a client.

Fields:

- `host` `string`: Stores the host value for the client.
- `password` `string`: Stores the password value for the client.
- `conn` `*grpc.ClientConn`: Stores the conn value for the client.
- `connErr` `error`: Stores the conn err value for the client.
- `once` `sync.Once`: Stores the once value for the client.

### `Offer`

Offer is a normalized DEX offer used by this package.

Fields:

- `ID` `string`: ID identifies the offer.
- `Direction` `string`: Direction is the direction value for the offer.
- `Price` `string`: Price is the price value used by the offer.
- `AmountSats` `uint64`: AmountSats is the amount in satoshis.
- `MinAmountSats` `uint64`: MinAmountSats is the amount in satoshis.
- `Volume` `string`: Volume is the volume value for the offer.
- `MinVolume` `string`: MinVolume is the min volume value for the offer.
- `PaymentAccountID` `string`: PaymentAccountID is the count associated with the offer.
- `PaymentMethodID` `string`: PaymentMethodID is the payment method id value for the offer.
- `PaymentMethodShortName` `string`: PaymentMethodShortName is the payment method short name value for the offer.
- `State` `string`: State is the state value for the offer.
- `IsActivated` `bool`: IsActivated reports whether the offer condition is true.
- `IsMyOffer` `bool`: IsMyOffer reports whether the offer condition is true.

### `PaymentAccount`

PaymentAccount represents a payment account.

Fields:

- `ID` `string`: ID identifies the payment account.
- `AccountName` `string`: AccountName is the count associated with the payment account.
- `PaymentMethodID` `string`: PaymentMethodID is the payment method id value for the payment account.
- `TradeCurrency` `string`: TradeCurrency names the asset or network for the payment account.

### `Trade`

Trade represents a trade.

Fields:

- `TradeID` `string`: TradeID is the trade id value for the trade.
- `OfferID` `string`: OfferID is the offer id value for the trade.
- `Role` `string`: Role is the role value for the trade.
- `State` `string`: State is the state value for the trade.
- `Phase` `string`: Phase is the phase value for the trade.
- `TradePrice` `string`: TradePrice is the price value used by the trade.
- `TradeVolume` `string`: TradeVolume is the trade volume value for the trade.
- `TradeAmountSats` `uint64`: TradeAmountSats is the amount in satoshis.
- `IsCompleted` `bool`: IsCompleted reports whether the trade condition is true.
- `HasFailed` `bool`: HasFailed reports whether the trade condition is true.
- `ErrorMessage` `string`: ErrorMessage carries a human-readable message for the trade.
- `DepositTxID` `string`: DepositTxID is the deposit tx id value for the trade.
- `PayoutTxID` `string`: PayoutTxID is the payout tx id value for the trade.
- `CreatedAt` `time.Time`: CreatedAt records the relevant time for the trade.

### `TakeOfferParams`

TakeOfferParams represents a take offer params.

Fields:

- `PaymentAccountID` `string`: PaymentAccountID is the count associated with the take offer params.
- `TakerFeeCurrencyCode` `string`: TakerFeeCurrencyCode names the asset or network for the take offer params.
- `AmountSats` `uint64`: AmountSats is the amount in satoshis.

### `CreatePositionParams`

CreatePositionParams represents a create position params.

Fields:

- `CurrencyCode` `string`: CurrencyCode names the asset or network for the create position params.
- `Direction` `string`: Direction is the direction value for the create position params.
- `Price` `string`: Price is the price value used by the create position params.
- `UseMarketBasedPrice` `bool`: UseMarketBasedPrice is the price value used by the create position params.
- `MarketPriceMarginPct` `float64`: MarketPriceMarginPct is a percentage value.
- `AmountSats` `uint64`: AmountSats is the amount in satoshis.
- `MinAmountSats` `uint64`: MinAmountSats is the amount in satoshis.
- `BuyerSecurityDepositPct` `float64`: BuyerSecurityDepositPct is a percentage value.
- `TriggerPrice` `string`: TriggerPrice is the price value used by the create position params.
- `PaymentAccountID` `string`: PaymentAccountID is the count associated with the create position params.
- `MakerFeeCurrencyCode` `string`: MakerFeeCurrencyCode names the asset or network for the create position params.

### `bisqCreds`

Fields:

- `password` `string`: Stores the password value for the bisq creds.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(host, password string) *Client`

### `(*Client).GetMyOffers`

GetMyOffers retrieves a my offers.

Signature: `func (c *Client) GetMyOffers(ctx context.Context, direction, currencyCode string) ([]Offer, error)`

### `(*Client).GetPaymentAccounts`

GetPaymentAccounts retrieves a payment accounts.

Signature: `func (c *Client) GetPaymentAccounts(ctx context.Context) ([]PaymentAccount, error)`

### `(*Client).TakeOffer`

TakeOffer accepts an existing DEX offer.

Signature: `func (c *Client) TakeOffer(ctx context.Context, offerID string, params TakeOfferParams) (*Trade, error)`

### `(*Client).GetOpenPositions`

GetOpenPositions retrieves an open positions.

Signature: `func (c *Client) GetOpenPositions(ctx context.Context) ([]Trade, error)`

### `(*Client).GetPosition`

GetPosition retrieves a position.

Signature: `func (c *Client) GetPosition(ctx context.Context, tradeID string) (*Trade, error)`

### `(*Client).CreatePosition`

CreatePosition creates a position.

Signature: `func (c *Client) CreatePosition(ctx context.Context, params CreatePositionParams) (*Offer, error)`

### `(*Client).invoke`

This function is part of the `bisq` package implementation.

Signature: `func (c *Client) invoke(ctx context.Context, method string, req, resp *dynamicpb.Message) error`

### `(*Client).connection`

This function is part of the `bisq` package implementation.

Signature: `func (c *Client) connection(ctx context.Context) (*grpc.ClientConn, error)`

### `(bisqCreds).GetRequestMetadata`

GetRequestMetadata retrieves a request metadata.

Signature: `func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error)`

### `(bisqCreds).RequireTransportSecurity`

RequireTransportSecurity reports whether the credentials require transport security.

Signature: `func (c bisqCreds) RequireTransportSecurity() bool`

### `fileDescriptor`

This function is part of the `bisq` package implementation.

Signature: `func fileDescriptor() protoreflect.FileDescriptor`

### `newMessage`

This function is part of the `bisq` package implementation.

Signature: `func newMessage(name string) *dynamicpb.Message`

### `setString`

This function is part of the `bisq` package implementation.

Signature: `func setString(msg *dynamicpb.Message, fieldName string, value string)`

### `setBool`

This function is part of the `bisq` package implementation.

Signature: `func setBool(msg *dynamicpb.Message, fieldName string, value bool)`

### `setFloat64`

This function is part of the `bisq` package implementation.

Signature: `func setFloat64(msg *dynamicpb.Message, fieldName string, value float64)`

### `setUint64`

This function is part of the `bisq` package implementation.

Signature: `func setUint64(msg *dynamicpb.Message, fieldName string, value uint64)`

### `setEnum`

This function is part of the `bisq` package implementation.

Signature: `func setEnum(msg *dynamicpb.Message, fieldName string, value int32)`

### `offerFromMessage`

This function is part of the `bisq` package implementation.

Signature: `func offerFromMessage(msg protoreflect.Message) Offer`

### `paymentAccountFromMessage`

This function is part of the `bisq` package implementation.

Signature: `func paymentAccountFromMessage(msg protoreflect.Message) PaymentAccount`

### `tradeFromMessage`

This function is part of the `bisq` package implementation.

Signature: `func tradeFromMessage(msg protoreflect.Message) Trade`

### `stringField`

This function is part of the `bisq` package implementation.

Signature: `func stringField(msg protoreflect.Message, name string) string`

### `boolField`

This function is part of the `bisq` package implementation.

Signature: `func boolField(msg protoreflect.Message, name string) bool`

### `uint64Field`

This function is part of the `bisq` package implementation.

Signature: `func uint64Field(msg protoreflect.Message, name string) uint64`

### `buildFileDescriptor`

This function is part of the `bisq` package implementation.

Signature: `func buildFileDescriptor() *descriptorpb.FileDescriptorProto`

### `message`

This function is part of the `bisq` package implementation.

Signature: `func message(name string, fields []*descriptorpb.FieldDescriptorProto, enums ...*descriptorpb.EnumDescriptorProto) *descriptorpb.DescriptorProto`

### `field`

This function is part of the `bisq` package implementation.

Signature: `func field(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto`

### `messageField`

This function is part of the `bisq` package implementation.

Signature: `func messageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto`

### `repeatedMessageField`

This function is part of the `bisq` package implementation.

Signature: `func repeatedMessageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto`

### `enumField`

This function is part of the `bisq` package implementation.

Signature: `func enumField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto`

### `method`

This function is part of the `bisq` package implementation.

Signature: `func method(name, input, output string) *descriptorpb.MethodDescriptorProto`

### `strptr`

This function is part of the `bisq` package implementation.

Signature: `func strptr(v string) *string`

### `int32ptr`

This function is part of the `bisq` package implementation.

Signature: `func int32ptr(v int32) *int32`

## Constants and Variables

- `defaultHost`: No inline declaration comment is present.
- `defaultCurrencyCode`: No inline declaration comment is present.
- `defaultTakerFeeCurrency`: No inline declaration comment is present.
- `tradesCategoryOpen`: No inline declaration comment is present.
- `bisqFileDesc`: No inline declaration comment is present.
- `bisqFileDescOnce`: No inline declaration comment is present.
- `bisqFileDescErr`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `errors`
- `fmt`
- `sync`
- `time`
- `google.golang.org/grpc`
- `google.golang.org/grpc/credentials/insecure`
- `google.golang.org/protobuf/reflect/protodesc`
- `google.golang.org/protobuf/reflect/protoreflect`
- `google.golang.org/protobuf/types/descriptorpb`
- `google.golang.org/protobuf/types/dynamicpb`

