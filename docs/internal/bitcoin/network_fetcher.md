# `internal/bitcoin/network_fetcher.go`

Source package: `bitcoin`  
Source lines: `60`

## Package Purpose

This file contributes to the `bitcoin` package.

## File Role

This file defines 1 type(s), implements 2 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `NetworkDataFetcher`

NetworkDataFetcher handles Bitcoin network data API calls via mempool.space.

Fields:

- `client` `*http.Client`: Stores the client value for the network data fetcher.
- `userAgent` `string`: Stores the user agent value for the network data fetcher.

## Functions and Methods

### `NewNetworkDataFetcher`

NewNetworkDataFetcher creates a new fetcher with mempool.space as the default source.

Signature: `func NewNetworkDataFetcher() *NetworkDataFetcher`

### `(*NetworkDataFetcher).FetchAddressBalance`

FetchAddressBalance returns the confirmed BTC balance of a Bitcoin address
by querying the mempool.space public API. The returned value is in BTC (not satoshis).

Signature: `func (ndf *NetworkDataFetcher) FetchAddressBalance(ctx context.Context, address string) (float64, error)`

## Key Dependencies

- `context`
- `encoding/json`
- `fmt`
- `net/http`
- `time`

