# `internal/openclaw/client.go`

Source package: `openclaw`  
Source lines: `224`

## Package Purpose

This file contributes to the `openclaw` package.

## File Role

This file defines 1 type(s), implements 8 function or method entry point(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates over a websocket connection.

## Types

### `Client`

Client represents a client.

Fields:

- `url` `string`: Stores an endpoint or address used by the client.
- `connMu` `sync.Mutex`: Stores the conn mu value for the client.
- `conn` `*websocket.Conn`: Stores the conn value for the client.
- `idSeq` `uint64`: Stores the id seq value for the client.
- `pendingMu` `sync.Mutex`: Stores the pending mu value for the client.
- `pending` `map[string]chan resMsg`: Stores the pending value for the client.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(url string) *Client`

### `(*Client).Connect`

Connect opens the websocket connection.

Signature: `func (c *Client) Connect(ctx context.Context) error`

### `(*Client).Close`

Close releases the underlying network resources.

Signature: `func (c *Client) Close() error`

### `(*Client).readLoop`

This function is part of the `openclaw` package implementation.

Signature: `func (c *Client) readLoop()`

### `(*Client).nextID`

This function is part of the `openclaw` package implementation.

Signature: `func (c *Client) nextID() string`

### `(*Client).Call`

Call sends an RPC request and decodes the response payload.

Signature: `func (c *Client) Call(ctx context.Context, method string, params any, out any) error`

### `(*Client).BankTxHasBeenSentToCex`

BankTxHasBeenSentToCex asks the gateway once whether it has already sent the
bank transaction with the given code to the CEX. It returns false without an
error when the gateway reports that the transfer has not arrived yet.

Signature: `func (c *Client) BankTxHasBeenSentToCex(ctx context.Context, code string) (bool, error)`

### `(*Client).PollBankTxArrivedFromDex`

PollBankTxArrivedFromDex periodically asks the (mock) claw gateway whether the bank
transaction with the given code has arrived yet.

Signature: `func (c *Client) PollBankTxArrivedFromDex(ctx context.Context, code string, interval time.Duration) (bool, error)`

## Key Dependencies

- `context`
- `encoding/json`
- `errors`
- `fmt`
- `sync`
- `sync/atomic`
- `time`
- `nhooyr.io/websocket`
- `nhooyr.io/websocket/wsjson`

