# healthcheck

## Purpose

The `healthcheck` package periodically reconciles incomplete transaction rows
with external systems. It advances lifecycle states when the venue provides a
safe confirmation, recovers interrupted tradeflow steps, and alerts an operator
when a structured DEX reports an anomalous or stalled trade.

The worker does not own every incomplete state. Live tradeflow, `dexrelay`, the
hedgeless refill worker, `dexFiatController`, and `dexrelease` retain ownership of
their respective stages.

## Main Flows

### Periodic dispatch

`RunPeriodic` calls `Run` immediately and then on every
`HEALTHCHECK_INTERVAL`. `Run` loads active transactions plus released rows that
are not independently verified, then dispatches each row by status. An error on
one row is logged without blocking the remaining rows.

| Status | Healthcheck behavior |
|---|---|
| `FiatToCexInitiated`, `FiatToCexArrived` | No-op; bank-transfer progression is external. |
| `CexTradeflowStarted` | After the recovery grace period, resume after a recorded spot fill or park an ambiguous fill for review. |
| `HedgeFailedNeedsReview` | Re-query the hedge venue and advance only if the hedge is actually open. |
| `CexTradeflowNeedsReview` | No-op; the row remains parked for an operator. |
| Hedgeless funding/refill states | No-op; owned by the refill worker. |
| `CexTradeSuccessful` | After the grace period, retry the withdrawal through the idempotent tradeflow recovery path. |
| CEX withdrawal states | No-op; owned by `dexrelay`. |
| `DexPositionOpen` | Check venue-specific take/chat state. Bisq failures are reported as anomalies. |
| `DexPositionTaken` | HodlHodl may advance when paid. Bisq polls for failures and the fiat-sent escalation described below. |
| `DexFiatHasArrived` | No-op; release is owned by `dexrelease`. |
| `DexPositionReleased` | HodlHodl completion is verified. Bisq requires daemon payout publication plus mempool/block visibility. RoboSats remains TODO. |

### Tradeflow recovery

`TradeFlow.Run` persists the spot buy, hedge, and withdrawal stages. If the
request dies between stages, healthcheck delegates recovery to the
consumer-side `TradeflowRecoverer` interface instead of duplicating execution
logic.

`CexTradeflowStarted` and `CexTradeSuccessful` rows must be older than
`TRADEFLOW_RECOVERY_GRACE` before recovery acts. This avoids racing a live
request. The recovery methods add deeper venue checks and idempotency guards.
`HedgeFailedNeedsReview` is already outside the live request and is checked
without that grace period.

### Bisq fiat-sent escalation

For a Bisq row that remains in `DexPositionTaken`, healthcheck makes one
`GetPosition` call per tick and checks both protocol failures and whether the
buyer marked fiat payment as sent. `Trade.FiatSent()` recognizes the daemon's
explicit payment-started boolean and compatible phase/state fallbacks.

The escalation timer starts when this process first observes the fiat-sent
signal. If the row is still pending after `BISQ_FIAT_ESCALATION_AFTER`, an
enabled Telegram anomaly notifier sends one alert asking the operator to verify
bank receipt manually. Successful alerts are deduplicated in memory. A restart
forgets both the first-observed time and the dedupe marker, so it restarts the
delay and may eventually produce one additional reminder.

This safety net never changes `tx.Status` and never releases BTC. The normal
`dexFiatController` evidence path remains responsible for advancing the row to
`DexFiatHasArrived`; `dexrelease` remains responsible for payout.

### Bisq payout verification

`dexrelease` records `DexPositionReleased` after submitting
`ConfirmPaymentReceived`; that status alone is not final verification.
Healthcheck continues loading the row while `TransactionComplete=false` and
requires all of the following before completing it:

1. the Bisq daemon reports payout publication;
2. the daemon exposes a payout txid that agrees with persisted metadata; and
3. the exact txid is visible in mempool or a block through the Bitcoin source.

Successful verification records the txid, verification time, chain status, and
confirmation count in execution metadata, then sets `TransactionComplete=true`.
Transitional states are retried. Bisq failures and txid conflicts alert the
operator once per process and never trigger another release attempt.

## Entry Points

### `NewHealthChecker`

```go
func NewHealthChecker(
    repo *db.TransactionRepository,
    btc *bitcoin.NetworkDataFetcher,
    hh *hodlhodl.Client,
    recoverer TradeflowRecoverer,
) *HealthChecker
```

The constructor sets a three-minute recovery grace period and a two-hour Bisq
fiat escalation delay. Builder methods attach the payment resolver, Telegram
relay/notifier, Bisq client, and configuration overrides.

### `RunPeriodic`

```go
func (h *HealthChecker) RunPeriodic(ctx context.Context, interval time.Duration)
```

Runs until the context is canceled. In `main.go`, it starts only when
`ENABLE_AUTOMATED_TRADEFLOW_WORKERS=true`.

### `Run`

```go
func (h *HealthChecker) Run(ctx context.Context) error
```

Loads and verifies active and released-but-unverified rows once. This is also
useful for a manual or test-triggered reconciliation pass.

## Operational Notes

| Variable | Default | Meaning |
|---|---:|---|
| `ENABLE_AUTOMATED_TRADEFLOW_WORKERS` | `false` | Starts healthcheck, DEX relay, and automated hedgeless workers when `true`. |
| `HEALTHCHECK_INTERVAL` | `30s` | Poll interval for incomplete transactions. |
| `TRADEFLOW_RECOVERY_GRACE` | `3m` | Minimum row age before interrupted live tradeflow recovery. |
| `BISQ_FIAT_ESCALATION_AFTER` | `2h` | Time from first observed Bisq fiat-sent signal to a manual-verification alert. Set to `0` or a negative duration to disable. |

Bisq alerts also require a configured, enabled Telegram anomaly notifier. If
Telegram is disabled or a send fails, the escalation is not consumed and will
be retried on a later tick after notification becomes available.
