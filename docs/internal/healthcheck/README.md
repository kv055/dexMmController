# healthcheck

## Purpose

The package periodically reconciles persisted trades with venue and Bitcoin
state. It also schedules recovery of interrupted CEX tradeflow stages.

## Main Flows

- Every row with `TransactionComplete=false` is dispatched to status-specific
  recovery, verification, or an explicit review-state no-op.
- Bisq trades waiting for fiat can produce a delayed manual-verification alert
  without authorizing release.
- Released Bisq rows remain eligible until the daemon reports payout
  publication and the exact payout txid is visible in mempool or a block.
- Successful post-release verification sets `TransactionComplete=true` and
  records chain evidence in execution metadata.
- Only bank-side evidence or explicit operator confirmation may advance a row
  to `DexFiatHasArrived`; venue paid/sent flags are informational.

See [`healthcheck.go`](healthcheck.md) for the detailed runtime flow.

## Entry Points

- `NewHealthChecker` constructs and wires the worker.
- `RunPeriodic` runs reconciliation until shutdown.
- `Run` executes one reconciliation pass.

## Operational Notes

The worker starts only with `ENABLE_AUTOMATED_TRADEFLOW_WORKERS=true` and polls
at `HEALTHCHECK_INTERVAL`. Bisq release verification requires both the daemon
client and Bitcoin transaction source. RoboSats post-release verification
remains out of scope.
