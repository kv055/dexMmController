# `internal/db/repository.go`

Source package: `db`  
Source lines: `367`

## Package Purpose

This file contributes to the `db` package.

## File Role

This file defines 2 type(s), implements 22 function or method entry point(s), declares 2 package value(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `TransactionRepository`

TransactionRepository provides database operations for transactions

Fields:

- `db` `*gorm.DB`: Stores the db value for the transaction repository.

### `TransactionFilters`

TransactionFilters represents a transaction filters.

Fields:

- `Limit` `int`: Limit is the limit value for the transaction filters.
- `Offset` `int`: Offset is the offset value for the transaction filters.
- `Status` `string`: Status is the current status of the transaction filters.
- `Direction` `string`: Direction is the direction value for the transaction filters.
- `CEX` `string`: CEX is the cex value for the transaction filters.
- `Dex` `string`: Dex is the dex value for the transaction filters.
- `IncompleteOnly` `bool`: IncompleteOnly is the incomplete only value for the transaction filters.

## Functions and Methods

### `(*TransactionRepository).GetAccountByProvider`

GetAccountByProvider retrieves an account by provider.

Signature: `func (r *TransactionRepository) GetAccountByProvider(ctx context.Context, provider string) (*Account, error)`

### `(*TransactionRepository).GetActiveAPIKeysForProvider`

GetActiveAPIKeysForProvider retrieves an active apikeys for provider.

Signature: `func (r *TransactionRepository) GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]APIKey, error)`

### `(*TransactionRepository).UpdateAccountHealthCheckedAt`

UpdateAccountHealthCheckedAt updates the timestamp of the latest account health check.

Signature: `func (r *TransactionRepository) UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error)`

### `NewTransactionRepository`

NewTransactionRepository creates a new repository instance

Signature: `func NewTransactionRepository(db *gorm.DB) *TransactionRepository`

### `(*TransactionRepository).CreateTransaction`

CreateTransaction creates a new transaction record

Signature: `func (r *TransactionRepository) CreateTransaction(tx *Transaction) error`

### `(*TransactionRepository).GetTransaction`

GetTransaction retrieves a single transaction by ID

Signature: `func (r *TransactionRepository) GetTransaction(id uint) (*Transaction, error)`

### `(*TransactionRepository).GetAllTransactions`

GetAllTransactions retrieves all transactions with optional pagination

Signature: `func (r *TransactionRepository) GetAllTransactions(limit int, offset int) ([]Transaction, error)`

### `(*TransactionRepository).ListTransactions`

ListTransactions returns transactions matching the provided filters.

Signature: `func (r *TransactionRepository) ListTransactions(filters TransactionFilters) ([]Transaction, int64, error)`

### `(*TransactionRepository).GetTransactionsByDirection`

GetTransactionsByDirection retrieves transactions filtered by direction (BUY/SELL)

Signature: `func (r *TransactionRepository) GetTransactionsByDirection(direction string) ([]Transaction, error)`

### `(*TransactionRepository).applyTransactionFilters`

This function is part of the `db` package implementation.

Signature: `func (r *TransactionRepository) applyTransactionFilters(query *gorm.DB, filters TransactionFilters) *gorm.DB`

### `(*TransactionRepository).GetTransactionsByCEX`

GetTransactionsByCEX retrieves transactions filtered by CEX name

Signature: `func (r *TransactionRepository) GetTransactionsByCEX(cexName string) ([]Transaction, error)`

### `(*TransactionRepository).UpdateTransaction`

UpdateTransaction updates an existing transaction

Signature: `func (r *TransactionRepository) UpdateTransaction(tx *Transaction) error`

### `(*TransactionRepository).DeleteTransaction`

DeleteTransaction deletes a transaction (soft delete)

Signature: `func (r *TransactionRepository) DeleteTransaction(id uint) error`

### `(*TransactionRepository).GetIncompleteTransactions`

GetIncompleteTransactions retrieves all transactions that have not yet reached DexPositionReleased

Signature: `func (r *TransactionRepository) GetIncompleteTransactions() ([]Transaction, error)`

### `(*TransactionRepository).GetTransactionCount`

GetTransactionCount returns the total number of transactions

Signature: `func (r *TransactionRepository) GetTransactionCount() (int64, error)`

### `(*TransactionRepository).GetPnlStats`

GetPnlStats returns PnL statistics

Signature: `func (r *TransactionRepository) GetPnlStats() (map[string]interface{}, error)`

### `(*TransactionRepository).GetWalletAddressByLabel`

GetWalletAddressByLabel retrieves a wallet address by label.

Signature: `func (r *TransactionRepository) GetWalletAddressByLabel(label string) (*WalletAddress, error)`

### `(*TransactionRepository).SaveWalletAddress`

SaveWalletAddress stores or updates a wallet address record.

Signature: `func (r *TransactionRepository) SaveWalletAddress(wallet *WalletAddress) error`

### `(*TransactionRepository).GetPrimaryWalletAddress`

GetPrimaryWalletAddress retrieves a primary wallet address.

Signature: `func (r *TransactionRepository) GetPrimaryWalletAddress() (*WalletAddress, error)`

### `(*TransactionRepository).UpdateAccountHealth`

UpdateAccountHealth records the latest health state for a provider.

Signature: `func (r *TransactionRepository) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error`

### `(*TransactionRepository).assignPrimaryWalletAddress`

This function is part of the `db` package implementation.

Signature: `func (r *TransactionRepository) assignPrimaryWalletAddress(tx *Transaction) error`

## Constants and Variables

- `LNDAccountProvider`: LNDAccountProvider is the lndaccount provider value.
- `ErrWalletAddressNotFound`: ErrWalletAddressNotFound is the err wallet address not found value.

## Key Dependencies

- `context`
- `database/sql`
- `errors`
- `fmt`
- `strings`
- `time`
- `gorm.io/gorm`
- `gorm.io/gorm/clause`
