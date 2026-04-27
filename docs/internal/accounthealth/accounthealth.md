# `internal/accounthealth/accounthealth.go`

Source package: `accounthealth`  
Source lines: `226`

## Package Purpose

This file contributes to the `accounthealth` package.

## File Role

This file defines 4 type(s), implements 9 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Repository`

Repository defines the behavior required by this package.

Definition: `interface { GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error) GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error) UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error) }`

### `Config`

Config represents a config.

Fields:

- `HodlHodlBaseURL` `string`: HodlHodlBaseURL is the hodl hodl base url value for the config.
- `BinanceBaseURL` `string`: BinanceBaseURL is the binance base url value for the config.
- `KrakenBaseURL` `string`: KrakenBaseURL is the kraken base url value for the config.
- `CoinbaseBaseURL` `string`: CoinbaseBaseURL is the coinbase base url value for the config.
- `RobosatsBaseURL` `string`: RobosatsBaseURL is the robosats base url value for the config.

### `Checker`

Checker represents a checker.

Fields:

- `repo` `Repository`: Stores the repo value for the checker.
- `cfg` `Config`: Stores the cfg value for the checker.

### `Result`

Result represents a result.

Fields:

- `Provider` `string`: Provider is the provider value for the result.
- `Status` `string`: Status is the current status of the result.
- `Healthy` `bool`: Healthy reports whether the result condition is true.
- `Message` `string`: Message carries a human-readable message for the result.
- `CheckedAt` `time.Time`: CheckedAt records the relevant time for the result.

## Functions and Methods

### `NewChecker`

NewChecker creates a checker.

Signature: `func NewChecker(repo Repository, cfg Config) *Checker`

### `SupportedProviders`

SupportedProviders returns the provider names checked by account health.

Signature: `func SupportedProviders() []string`

### `CanonicalProvider`

CanonicalProvider maps provider aliases to their canonical names.

Signature: `func CanonicalProvider(raw string) (string, error)`

### `(*Checker).CheckProvider`

CheckProvider checks a provider.

Signature: `func (c *Checker) CheckProvider(ctx context.Context, rawProvider string) (Result, error)`

### `(*Checker).CheckAll`

CheckAll checks an all.

Signature: `func (c *Checker) CheckAll(ctx context.Context) []Result`

### `(*Checker).RunPeriodic`

RunPeriodic runs a periodic.

Signature: `func (c *Checker) RunPeriodic(ctx context.Context, interval time.Duration)`

### `(*Checker).checkActiveKeys`

This function is part of the `accounthealth` package implementation.

Signature: `func (c *Checker) checkActiveKeys(ctx context.Context, provider string, keys []db.APIKey) error`

### `(*Checker).checkKey`

This function is part of the `accounthealth` package implementation.

Signature: `func (c *Checker) checkKey(ctx context.Context, provider string, key db.APIKey) error`

### `ParseInterval`

ParseInterval parses an interval.

Signature: `func ParseInterval(raw string) time.Duration`

## Constants and Variables

- `ErrUnsupportedProvider`: ErrUnsupportedProvider is the err unsupported provider value.

## Key Dependencies

- `context`
- `errors`
- `fmt`
- `log`
- `strings`
- `time`
- `dexMmController/internal/db`
- `dexMmController/internal/exchanges/cex/binance`
- `dexMmController/internal/exchanges/cex/coinbase`
- `dexMmController/internal/exchanges/cex/kraken`
- `dexMmController/internal/exchanges/dex/hodlhodl`
- `dexMmController/internal/exchanges/dex/robosats`

