# `internal/openclaw/mock_server.go`

Source package: `openclaw`  
Source lines: `133`

## Package Purpose

This file contributes to the `openclaw` package.

## File Role

This file defines 1 type(s), implements 6 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.
- Communicates over a websocket connection.

## Types

### `MockServer`

MockServer represents a mock server.

Fields:

- `Addr` `string`: Addr is the wallet or peer address used by the mock server.
- `mu` `sync.Mutex`: For each code, treat it as "arrived" once time.Now() >= arriveAt.
- `arriveAt` `map[string]time.Time`: Stores timing information for the mock server.
- `srv` `*http.Server`: Stores the srv value for the mock server.
- `ln` `net.Listener`: Stores the ln value for the mock server.

## Functions and Methods

### `NewMockServer`

NewMockServer creates a mock server.

Signature: `func NewMockServer(addr string) *MockServer`

### `(*MockServer).SetArrivedAfter`

SetArrivedAfter configures when a specific code becomes "arrived".

Signature: `func (m *MockServer) SetArrivedAfter(code string, d time.Duration)`

### `(*MockServer).Start`

Start starts the configured worker.

Signature: `func (m *MockServer) Start() error`

### `(*MockServer).WSURL`

WSURL returns the websocket URL for the mock server.

Signature: `func (m *MockServer) WSURL() string`

### `(*MockServer).Close`

Close releases the underlying network resources.

Signature: `func (m *MockServer) Close(ctx context.Context) error`

### `(*MockServer).handleWS`

This function is part of the `openclaw` package implementation.

Signature: `func (m *MockServer) handleWS(w http.ResponseWriter, r *http.Request)`

## Key Dependencies

- `context`
- `encoding/json`
- `net`
- `net/http`
- `sync`
- `time`
- `nhooyr.io/websocket`
- `nhooyr.io/websocket/wsjson`

