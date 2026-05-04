# `internal/db/database.go`

Source package: `db`  
Source lines: `41`

## Package Purpose

This file contributes to the `db` package.

## File Role

This file implements 2 function or method entry point(s).

## Runtime Behavior

- Reads from or writes to persistent storage.

## Functions and Methods

### `InitDB`

InitDB connects to the shared Postgres database owned by dexUnifiedApi.
Schema creation is intentionally handled by the unified API.

Signature: `func InitDB(databaseURL string) (*gorm.DB, error)`

### `Close`

Close closes the database connection

Signature: `func Close(db *gorm.DB) error`

## Key Dependencies

- `fmt`
- `log`
- `gorm.io/driver/postgres`
- `gorm.io/gorm`
