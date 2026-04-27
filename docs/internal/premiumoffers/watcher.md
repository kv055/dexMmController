# `internal/premiumoffers/watcher.go`

Source package: `premiumoffers`  
Source lines: `524`

## Package Purpose

This file contributes to the `premiumoffers` package.

## File Role

This file defines 2 type(s), implements 24 function or method entry point(s), declares 2 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates over a websocket connection.

## Types

### `Watcher`

Watcher represents a watcher.

Fields:

- `repo` `*db.TransactionRepository`: Stores the repo value for the watcher.
- `bisq` `*bisq.Client`: Stores the bisq value for the watcher.
- `hodlhodl` `*hodlhodl.Client`: Stores the hodlhodl value for the watcher.
- `robosats` `*robosats.Client`: Stores a Bitcoin-denominated amount or limit for the watcher.
- `url` `string`: Stores an endpoint or address used by the watcher.
- `netProfitThreshold` `float64`: Stores the net profit threshold value for the watcher.
- `minQualifiedOffers` `int`: Stores the min qualified offers value for the watcher.
- `lastLoggedSignature` `string`: Stores the last logged signature value for the watcher.

### `Offer`

Offer is a normalized DEX offer used by this package.

Fields:

- `ID` `int64`: ID identifies the offer.
- `Exchange` `string`: Exchange is the exchange value for the offer.
- `Direction` `string`: Direction is the direction value for the offer.
- `Price` `float64`: Price is the price value used by the offer.
- `PriceDiffPercent` `*float64`: PriceDiffPercent is a percentage value.
- `TradeCostPercent` `*float64`: TradeCostPercent is a percentage value.
- `NetPremiumPercent` `*float64`: NetPremiumPercent is a percentage value.
- `BTCMin` `*float64`: BTCMin is a BTC-denominated btcmin value.
- `BTCMax` `*float64`: BTCMax is a BTC-denominated btcmax value.
- `PaymentMethod` `*string`: PaymentMethod is the payment method value for the offer.
- `OfferID` `*string`: OfferID is the offer id value for the offer.
- `FirstSeen` `*string`: FirstSeen is the first seen value for the offer.
- `LastSeen` `*string`: LastSeen is the last seen value for the offer.
- `RefPrice` `*float64`: RefPrice is the price value used by the offer.

## Functions and Methods

### `NewWatcher`

NewWatcher creates a watcher.

Signature: `func NewWatcher( repo *db.TransactionRepository, bisqClient *bisq.Client, hodlhodlClient *hodlhodl.Client, robosatsClient *robosats.Client, streamURL string, ) *Watcher`

### `(*Watcher).Run`

Run runs value.

Signature: `func (w *Watcher) Run(ctx context.Context) error`

### `(*Watcher).runSession`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) runSession(ctx context.Context) error`

### `qualifiedOffers`

This function is part of the `premiumoffers` package implementation.

Signature: `func qualifiedOffers(v any, threshold float64) []Offer`

### `offersWithPremium`

This function is part of the `premiumoffers` package implementation.

Signature: `func offersWithPremium(v any) []map[string]any`

### `collectOffersWithPremium`

This function is part of the `premiumoffers` package implementation.

Signature: `func collectOffersWithPremium(v any, out *[]map[string]any)`

### `hasPremiumField`

This function is part of the `premiumoffers` package implementation.

Signature: `func hasPremiumField(m map[string]any) bool`

### `floatValue`

This function is part of the `premiumoffers` package implementation.

Signature: `func floatValue(v any) (float64, bool)`

### `offerSignature`

This function is part of the `premiumoffers` package implementation.

Signature: `func offerSignature(offers []Offer) string`

### `buildTransaction`

This function is part of the `premiumoffers` package implementation.

Signature: `func buildTransaction(streamURL string, offers []Offer) (*db.Transaction, error)`

### `bestOffer`

This function is part of the `premiumoffers` package implementation.

Signature: `func bestOffer(offers []Offer) Offer`

### `netPremium`

This function is part of the `premiumoffers` package implementation.

Signature: `func netPremium(offer Offer) float64`

### `inferDexPlatform`

This function is part of the `premiumoffers` package implementation.

Signature: `func inferDexPlatform(exchange string) db.DexPlatform`

### `inferCexFromStreamURL`

This function is part of the `premiumoffers` package implementation.

Signature: `func inferCexFromStreamURL(raw string) db.CexExchange`

### `(*Watcher).takeBestOffer`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) takeBestOffer(ctx context.Context, tx *db.Transaction, offers []Offer) error`

### `(*Watcher).takeHodlHodlOffer`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) takeHodlHodlOffer(ctx context.Context, offer Offer) (map[string]any, string, error)`

### `(*Watcher).takeBisqOffer`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) takeBisqOffer(ctx context.Context, offer Offer) (map[string]any, string, error)`

### `(*Watcher).takeRoboSatsOffer`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) takeRoboSatsOffer(ctx context.Context, offer Offer) (map[string]any, string, error)`

### `(*Watcher).latestRoboSatsToken`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) latestRoboSatsToken(ctx context.Context) (string, error)`

### `(*Watcher).updateExecutionPayload`

This function is part of the `premiumoffers` package implementation.

Signature: `func (w *Watcher) updateExecutionPayload(tx *db.Transaction, selected Offer, result map[string]any, executionErr error) error`

### `offerFiatAmount`

This function is part of the `premiumoffers` package implementation.

Signature: `func offerFiatAmount(offer Offer) float64`

### `offerBTCAmount`

This function is part of the `premiumoffers` package implementation.

Signature: `func offerBTCAmount(offer Offer) float64`

### `btcToSats`

This function is part of the `premiumoffers` package implementation.

Signature: `func btcToSats(amount float64) uint64`

### `errorsPayload`

This function is part of the `premiumoffers` package implementation.

Signature: `func errorsPayload(tx *db.Transaction, message string) error`

## Constants and Variables

- `defaultNetProfitThreshold`: No inline declaration comment is present.
- `defaultMinQualifiedOffers`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `crypto/sha256`
- `encoding/hex`
- `encoding/json`
- `fmt`
- `log`
- `net/url`
- `sort`
- `strconv`
- `strings`
- `time`
- `dexMmController/internal/db`
- `dexMmController/internal/exchanges/dex/bisq`
- `dexMmController/internal/exchanges/dex/hodlhodl`
- `dexMmController/internal/exchanges/dex/robosats`
- `nhooyr.io/websocket`
- `nhooyr.io/websocket/wsjson`

