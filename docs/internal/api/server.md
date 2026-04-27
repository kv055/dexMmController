# `internal/api/server.go`

Source package: `api`  
Source lines: `264`

## Package Purpose

This file contributes to the `api` package.

## File Role

This file defines 5 type(s), implements 13 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Server`

Server represents a server.

Fields:

- `addr` `string`: Stores an endpoint or address used by the server.
- `repo` `*db.TransactionRepository`: Stores the repo value for the server.
- `accountHealth` `accountHealthChecker`: Stores the account health value for the server.

### `accountHealthChecker`

Definition: `interface { CheckProvider(ctx context.Context, provider string) (accounthealth.Result, error) }`

### `transactionListResponse`

Fields:

- `Transactions` `[]db.Transaction`: Stores the transactions value for the transaction list response.
- `Total` `int64`: Stores the total value for the transaction list response.
- `Limit` `int`: Stores the limit value for the transaction list response.
- `Offset` `int`: Stores the offset value for the transaction list response.

### `healthResponse`

Fields:

- `Status` `string`: Stores the current status for the health response.
- `TransactionCount` `int64`: Stores the transaction count value for the health response.
- `IncompleteCount` `int`: Stores the incomplete count value for the health response.
- `PrimaryWalletPresent` `bool`: Stores the primary wallet present value for the health response.

### `accountHealthResponse`

Fields:

- `Provider` `string`: Stores the provider value for the account health response.
- `Status` `string`: Stores the current status for the account health response.
- `Healthy` `bool`: Reports a boolean condition for the account health response.
- `Message` `string`: Stores the message value for the account health response.
- `CheckedAt` `string`: Stores timing information for the account health response.

## Functions and Methods

### `NewServer`

NewServer creates a server.

Signature: `func NewServer(addr string, repo *db.TransactionRepository) *Server`

### `(*Server).WithAccountHealth`

WithAccountHealth attaches the account health service to the API server.

Signature: `func (s *Server) WithAccountHealth(checker accountHealthChecker) *Server`

### `(*Server).Run`

Run runs value.

Signature: `func (s *Server) Run(ctx context.Context) error`

### `(*Server).handleAccountHealthCheck`

This function is part of the `api` package implementation.

Signature: `func (s *Server) handleAccountHealthCheck(w http.ResponseWriter, r *http.Request)`

### `(*Server).handleHealth`

This function is part of the `api` package implementation.

Signature: `func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request)`

### `(*Server).handleTransactions`

This function is part of the `api` package implementation.

Signature: `func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request)`

### `(*Server).handleTransactionByID`

This function is part of the `api` package implementation.

Signature: `func (s *Server) handleTransactionByID(w http.ResponseWriter, r *http.Request)`

### `(*Server).handleStats`

This function is part of the `api` package implementation.

Signature: `func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request)`

### `corsMiddleware`

This function is part of the `api` package implementation.

Signature: `func corsMiddleware(next http.Handler) http.Handler`

### `queryInt`

This function is part of the `api` package implementation.

Signature: `func queryInt(r *http.Request, key string, fallback int) int`

### `queryBool`

This function is part of the `api` package implementation.

Signature: `func queryBool(r *http.Request, key string) bool`

### `writeError`

This function is part of the `api` package implementation.

Signature: `func writeError(w http.ResponseWriter, status int, err error)`

### `writeJSON`

This function is part of the `api` package implementation.

Signature: `func writeJSON(w http.ResponseWriter, status int, payload any)`

## Key Dependencies

- `context`
- `encoding/json`
- `errors`
- `fmt`
- `log`
- `net/http`
- `strconv`
- `strings`
- `time`
- `dexMmController/internal/accounthealth`
- `dexMmController/internal/db`

