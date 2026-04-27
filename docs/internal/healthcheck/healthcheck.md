# `internal/healthcheck/healthcheck.go`

Source package: `healthcheck`  
Source lines: `313`

## Package Purpose

This file contributes to the `healthcheck` package.

## File Role

This file defines 1 type(s), implements 14 function or method entry point(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `HealthChecker`

HealthChecker queries the DB for incomplete transactions and verifies their
actual state against external systems, advancing the status where appropriate.

Fields:

- `repo` `*db.TransactionRepository`: Stores the repo value for the health checker.
- `openclaw` `*openclaw.Client`: Stores the openclaw value for the health checker.
- `hummingbot` `*hummingbot.Client`: Stores the hummingbot value for the health checker.
- `btc` `*bitcoin.NetworkDataFetcher`: Stores a Bitcoin-denominated amount or limit for the health checker.
- `hodlhodl` `*hodlhodl.Client`: Stores the hodlhodl value for the health checker.

## Functions and Methods

### `NewHealthChecker`

NewHealthChecker creates a health checker.

Signature: `func NewHealthChecker(repo *db.TransactionRepository, oc *openclaw.Client, hb *hummingbot.Client, btc *bitcoin.NetworkDataFetcher, hh *hodlhodl.Client) *HealthChecker`

### `(*HealthChecker).Run`

Run finds all incomplete transactions and runs a status-specific verification
for each one.

Signature: `func (h *HealthChecker) Run(ctx context.Context) error`

### `(*HealthChecker).verify`

verify dispatches to the appropriate scenario based on the current status.

Signature: `func (h *HealthChecker) verify(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyCexTradeSuccessful`

verifyCexTradeSuccessful confirms that a BTC buy trade occurred on the CEX
after the fiat deposit arrived and that a positive BTC balance is now
visible. If both conditions hold the transaction is advanced to
StatusCexTradeSuccessful.

Signature: `func (h *HealthChecker) verifyCexTradeSuccessful(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyCexCryptoWithdrawalInitiated`

verifyCexCryptoWithdrawalInitiated checks whether the on-chain withdrawal
has been confirmed by the CEX.

TODO: the Hummingbot Backend API (v1.0.1) has no withdrawal endpoint.
Implement once the API exposes withdrawal history.
Only use in worst case scenario when verifyCexCryptoWithdrawalArrived fails
to detect the transaction on chain (confirmed or unconfirmed).

Signature: `func (h *HealthChecker) verifyCexCryptoWithdrawalInitiated(_ context.Context, _ *db.Transaction) error`

### `(*HealthChecker).verifyCexCryptoWithdrawalArrived`

verifyCexCryptoWithdrawalArrived checks whether the BTC withdrawn from the
CEX has arrived at the DEX wallet (tx.DexBtcAddress) by querying the
confirmed on-chain balance via mempool.space. The expected amount is read
from tx.CexBitcoinAmount. A 1 % tolerance is applied to absorb miner fees.
Advances to StatusCexCryptoWithdrawalArrived once the balance matches.

Signature: `func (h *HealthChecker) verifyCexCryptoWithdrawalArrived(ctx context.Context, tx *db.Transaction) error`

### `dexOfferID`

dexOfferID extracts the offer_id from the DexOfferCreation JSON blob.

Signature: `func dexOfferID(tx *db.Transaction) (string, error)`

### `dexContractID`

dexContractID extracts the contract_id from the DexOfferCreation JSON blob.

Signature: `func dexContractID(tx *db.Transaction) (string, error)`

### `(*HealthChecker).verifyDexPositionOpen`

verifyDexPositionOpen checks the DEX (tx.Dex) to confirm the offer is still
live. Advances to DexPositionTaken if a counterparty has already matched.

Signature: `func (h *HealthChecker) verifyDexPositionOpen(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyDexPositionTaken`

verifyDexPositionTaken confirms the trade is in progress on the DEX.
For HodlHodl, checks whether the buyer has marked the contract as paid (fiat sent).

Signature: `func (h *HealthChecker) verifyDexPositionTaken(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyDexFiatHasArrived`

verifyDexFiatHasArrived asks the local openclaw instance whether the fiat
payment has actually arrived in the bank account. Advances to
DexPositionReleased if confirmed, so the BTC release can be triggered.

Signature: `func (h *HealthChecker) verifyDexFiatHasArrived(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyDexPositionReleased`

verifyDexPositionReleased connects to the DEX specified in tx.Dex and checks
whether the BTC has actually been released to the buyer.

Signature: `func (h *HealthChecker) verifyDexPositionReleased(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyFiatToCexInitiated`

verifyFiatToCexInitiated asks openclaw (the bank gateway) whether the fiat
wire to the CEX has been sent. The transaction ID is used as the openclaw
reference code, matching the pattern used for DEX fiat checks.

Signature: `func (h *HealthChecker) verifyFiatToCexInitiated(ctx context.Context, tx *db.Transaction) error`

### `(*HealthChecker).verifyFiatToCexArrived`

verifyFiatToCexArrived confirms the fiat balance on the CEX is at least the
expected amount. Uses tx.FiatCurrency and tx.Cex from the DB to identify
which asset and exchange to query via Hummingbot.

Signature: `func (h *HealthChecker) verifyFiatToCexArrived(ctx context.Context, tx *db.Transaction) error`

## Key Dependencies

- `context`
- `encoding/json`
- `fmt`
- `time`
- `dexMmController/internal/bitcoin`
- `dexMmController/internal/db`
- `dexMmController/internal/exchanges/dex/hodlhodl`
- `dexMmController/internal/hummingbot`
- `dexMmController/internal/openclaw`

