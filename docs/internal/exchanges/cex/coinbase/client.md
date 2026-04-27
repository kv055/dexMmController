# `internal/exchanges/cex/coinbase/client.go`

Source package: `coinbase`  
Source lines: `153`

## Package Purpose

This file contributes to the `coinbase` package.

## File Role

This file defines 1 type(s), implements 7 function or method entry point(s), declares 2 package value(s).

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
- `keyName` `string`: Stores the key name value for the client.
- `privateKey` `string`: Stores the private key value for the client.
- `httpClient` `*http.Client`: Stores the http client value for the client.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(baseURL, keyName, privateKey string) *Client`

### `(*Client).KeyPermissions`

KeyPermissions retrieves Coinbase key permissions.

Signature: `func (c *Client) KeyPermissions(ctx context.Context) error`

### `(*Client).buildJWT`

This function is part of the `coinbase` package implementation.

Signature: `func (c *Client) buildJWT(method, path string) (string, error)`

### `parseECPrivateKey`

This function is part of the `coinbase` package implementation.

Signature: `func parseECPrivateKey(raw string) (*ecdsa.PrivateKey, error)`

### `encodeJWTParts`

This function is part of the `coinbase` package implementation.

Signature: `func encodeJWTParts(header, payload any) (string, error)`

### `padBigInt`

This function is part of the `coinbase` package implementation.

Signature: `func padBigInt(v *big.Int, size int) []byte`

### `randomHex`

This function is part of the `coinbase` package implementation.

Signature: `func randomHex(size int) string`

## Constants and Variables

- `defaultBaseURL`: No inline declaration comment is present.
- `requestHost`: No inline declaration comment is present.

## Key Dependencies

- `context`
- `crypto/ecdsa`
- `crypto/rand`
- `crypto/sha256`
- `crypto/x509`
- `encoding/base64`
- `encoding/hex`
- `encoding/json`
- `encoding/pem`
- `fmt`
- `io`
- `math/big`
- `net/http`
- `strings`
- `time`

