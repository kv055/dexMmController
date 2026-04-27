# `internal/db/wallet_addresses.go`

Source package: `db`  
Source lines: `23`

## Package Purpose

This file contributes to the `db` package.

## File Role

This file defines 1 type(s), implements 1 function or method entry point(s).

## Runtime Behavior

- Reads from or writes to persistent storage.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.

## Types

### `WalletAddress`

WalletAddress stores reusable wallet addresses in cleartext for operational use.

Fields:

- `ID` `uint`: ID identifies the wallet address.
- `Label` `string`: Label is the label value for the wallet address.
- `Address` `string`: Address is the wallet or peer address used by the wallet address.
- `CreatedAt` `time.Time`: CreatedAt records the relevant time for the wallet address.
- `UpdatedAt` `time.Time`: UpdatedAt records the relevant time for the wallet address.

## Functions and Methods

### `(WalletAddress).TableName`

TableName returns the database table name for the model.

Signature: `func (WalletAddress) TableName() string`

## Key Dependencies

- `time`

