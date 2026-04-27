# `main.go`

Source package: `main`  
Source lines: `404`

## Package Purpose

This file contributes to the `main` package.

## File Role

This file defines 3 type(s), implements 13 function or method entry point(s).

## Runtime Behavior

- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `robosatsTokenRepo`

Definition: `interface { GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error) }`

### `lndHealthClient`

Definition: `interface { HealthCheck(ctx context.Context) error }`

### `lndAccountHealthRepository`

Definition: `interface { GetPrimaryWalletAddress() (*db.WalletAddress, error) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error }`

## Functions and Methods

### `main`

This function is part of the `main` package implementation.

Signature: `func main()`

### `envOr`

envOr returns the value of the environment variable key, or fallback if unset.

Signature: `func envOr(key, fallback string) string`

### `robosatsAuthTokenFromDB`

This function is part of the `main` package implementation.

Signature: `func robosatsAuthTokenFromDB(ctx context.Context, repo robosatsTokenRepo, fallback string) (string, error)`

### `mustEnv`

mustEnv returns the value of the environment variable key or exits if unset.

Signature: `func mustEnv(key string) string`

### `ensurePrimaryDepositAddress`

This function is part of the `main` package implementation.

Signature: `func ensurePrimaryDepositAddress( ctx context.Context, repo *db.TransactionRepository, lndHost, tlsCertPath, macaroonPath, password string, initCfg lnd.InitConfig, ) (string, *lnd.Client, error)`

### `checkLNDAccountHealth`

This function is part of the `main` package implementation.

Signature: `func checkLNDAccountHealth(ctx context.Context, repo lndAccountHealthRepository, client lndHealthClient) error`

### `runLNDAccountHealthCheck`

This function is part of the `main` package implementation.

Signature: `func runLNDAccountHealthCheck(ctx context.Context, repo lndAccountHealthRepository, client lndHealthClient)`

### `runPeriodicLNDAccountHealthCheck`

This function is part of the `main` package implementation.

Signature: `func runPeriodicLNDAccountHealthCheck(ctx context.Context, interval time.Duration, repo lndAccountHealthRepository, client lndHealthClient)`

### `parseAccountHealthInterval`

This function is part of the `main` package implementation.

Signature: `func parseAccountHealthInterval(raw string) time.Duration`

### `loadLNDInitConfig`

This function is part of the `main` package implementation.

Signature: `func loadLNDInitConfig( seedMnemonicFile, aezeedPassphraseFile, extendedMasterKeyFile, birthdayTimestampFile, recoveryWindowRaw, birthdayTimestampRaw string, ) (lnd.InitConfig, error)`

### `readSecretFile`

This function is part of the `main` package implementation.

Signature: `func readSecretFile(path string) (string, error)`

### `recordLNDMasterKeyHash`

This function is part of the `main` package implementation.

Signature: `func recordLNDMasterKeyHash(repo *db.TransactionRepository, path string) error`

### `hashFileSHA256`

This function is part of the `main` package implementation.

Signature: `func hashFileSHA256(path string) (string, int64, error)`

## Key Dependencies

- `context`
- `crypto/sha256`
- `encoding/hex`
- `fmt`
- `log`
- `os`
- `os/signal`
- `strconv`
- `strings`
- `syscall`
- `time`
- `dexMmController/internal/accounthealth`
- `dexMmController/internal/api`
- `dexMmController/internal/bitcoin`
- `dexMmController/internal/db`
- `dexMmController/internal/exchanges/dex/bisq`
- `dexMmController/internal/exchanges/dex/hodlhodl`
- `dexMmController/internal/exchanges/dex/robosats`
- `dexMmController/internal/healthcheck`
- `dexMmController/internal/hummingbot`
- `dexMmController/internal/openclaw`
- `dexMmController/internal/premiumoffers`
- `dexMmController/internal/wallethub`

