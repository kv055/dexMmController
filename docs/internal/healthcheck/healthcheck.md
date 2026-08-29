# `healthcheck.go`

## Purpose

`internal/healthcheck/healthcheck.go` reconciles persisted trade state with DEX,
Bitcoin, and recovery-system state. It advances rows only when an external
signal meets the relevant safety bar and leaves transitional states for a later
poll.

## Runtime Role

`main.go` starts `RunPeriodic` when automated tradeflow workers are enabled.
Each pass loads all rows whose `TransactionComplete` flag is false. Errors are
logged per transaction so one unavailable venue does not block unrelated rows.

The worker schedules recovery and verification; money-moving operations remain
owned by tradeflow, `dexrelay`, and `dexrelease`.

## Function Map

### `NewHealthChecker`

```go
func NewHealthChecker(repo *db.TransactionRepository, btc *bitcoin.NetworkDataFetcher, hh *hodlhodl.Client, recoverer TradeflowRecoverer) *HealthChecker
```

Creates the checker with default recovery and Bisq escalation delays. Narrow
internal interfaces for the repository, Bisq position lookup, Bitcoin
transaction lookup, and anomaly delivery keep verification testable while the
runtime constructor accepts the concrete service clients.

### `Run`

```go
func (h *HealthChecker) Run(ctx context.Context) error
```

Loads `GetTransactionsForHealthcheck` and dispatches every row by status.
Released-but-unverified rows are included so venue release submission and
independent completion verification remain separate lifecycle events. Terminal
preparation failures are excluded because their completion flag is true.

Review statuses (`TransactionNeedsReview`, `HedgeFailedNeedsReview`,
`CexTradeflowNeedsReview`, `LiquidityPreparationNeedsReview`,
`HedgelessFundingNeedsReview`, and `DexTakenFundingNeedsReview`) are explicitly
handled. The checker never falls through and accidentally treats them as an
unknown state.

### `verifyDexPositionTaken`

```go
func (h *HealthChecker) verifyDexPositionTaken(ctx context.Context, tx *db.Transaction) error
```

Observes venue state for a taken and durably funded DEX trade. A HodlHodl
`paid` status and Bisq fiat-sent state can trigger monitoring or delayed anomaly
alerts, but cannot set `DexFiatHasArrived`. That transition is reserved for the
fiat controller after matched bank evidence or an explicit operator decision.

### `verifyBisqPositionTaken`

```go
func (h *HealthChecker) verifyBisqPositionTaken(ctx context.Context, tx *db.Transaction) error
```

Fetches the Bisq trade once, reports daemon failures, and observes
`Trade.FiatSent()`. A fiat-sent signal that remains unmatched after the
configured delay produces one manual-verification alert; it never changes the
transaction status or releases BTC.

### `verifyDexPositionReleased`

```go
func (h *HealthChecker) verifyDexPositionReleased(ctx context.Context, tx *db.Transaction) error
```

Dispatches post-release verification. HodlHodl uses its completed-contract
state. Bisq delegates to `verifyBisqPositionReleased`. RoboSats release
verification remains unimplemented and leaves its row unchanged.

### `verifyBisqPositionReleased`

```go
func (h *HealthChecker) verifyBisqPositionReleased(ctx context.Context, tx *db.Transaction) error
```

Treats `DexPositionReleased` as release submission, not final verification. It:

1. loads the trade identified by persisted execution metadata;
2. requires `Trade.PayoutPublished()` and a daemon payout txid;
3. rejects a conflict with an already-persisted release txid;
4. requires that exact txid to be visible in mempool or a block through the
   Bitcoin transaction source;
5. records verification time, chain status, and confirmations; and
6. sets `TransactionComplete=true` in the same repository update.

Not-yet-published and not-yet-visible results are normal retry states. Client,
decoding, and persistence errors are returned for per-row logging. Bisq
failures and txid mismatches produce deduplicated operator anomalies and never
complete the row. This verifier never calls `ConfirmPaymentReceived` again.

## Failure Behavior

- One transaction error does not stop the periodic pass.
- Expected protocol transitions remain unchanged for the next tick.
- Release anomalies are deduplicated only for the process lifetime; a restart
  can eventually send another alert.
- Disabled or failed anomaly delivery is not marked as delivered.
- Missing Bisq or Bitcoin dependencies prevent completion rather than falling
  back to daemon-only trust.
