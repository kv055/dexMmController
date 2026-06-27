# healthcheck

The `healthcheck` package is a periodic background worker that reconciles
**incomplete transactions** against the outside world. Every tick it loads each
unfinished `db.Transaction`, checks what *actually* happened on the relevant
external system (the CEX, the Bitcoin network, the DEX, or the bank gateway), and
advances the row's `Status` when reality says it should move forward.

It plays two roles:

1. **Lifecycle progression** — pushing healthy trades through the DEX-side states
   (counterparty joined → fiat paid → fiat arrived in bank → BTC released).
2. **Tradeflow recovery** — finishing or flagging trades whose CEX "buy leg"
   (`TradeFlow.Run`) failed or was interrupted, which is the focus of the recovery
   logic described below.

---

## How it is wired into `main.go`

The checker is constructed once at startup and injected with everything it needs.
It is intentionally built **after** `tradeFlow`, because it depends on it for
recovery (no dependency cycle: `tradeFlow` needs `repo` + `cexAccountService`;
`healthcheck` needs `tradeFlow`).

```go
// main.go
healthcheckInterval := parseAccountHealthInterval(envOr("HEALTHCHECK_INTERVAL", "30s"))
// ...
tradeFlow := tradeflow.New(cexAccountService, repo)

hc := healthcheck.NewHealthChecker(repo, claw, btc, hh, tradeFlow).
    WithRecoveryGracePeriod(parseAccountHealthInterval(envOr("TRADEFLOW_RECOVERY_GRACE", "3m")))
// ...
go hc.RunPeriodic(ctx, healthcheckInterval)   // currently commented out — see "Status" below
```

### Constructor dependencies

`NewHealthChecker(repo, oc, btc, hh, recoverer)`:

| Param | Type | Used for |
|-------|------|----------|
| `repo` | `*db.TransactionRepository` | load incomplete transactions, persist status updates |
| `oc` | `*openclaw.Client` | bank gateway — has fiat been sent to / arrived from the DEX |
| `btc` | `*bitcoin.NetworkDataFetcher` | on-chain confirmation checks |
| `hh` | `*hodlhodl.Client` | DEX contract state (taken / paid / released) |
| `recoverer` | `TradeflowRecoverer` | drives recovery of stuck tradeflow rows; **may be `nil`** to disable recovery (those states then no-op instead of erroring) |

`recoverer` is a small consumer-side interface declared in this package; it is
satisfied by `*tradeflow.TradeFlow`. Declaring it here keeps healthcheck depending
only on the three methods it actually calls.

### Configuration (env)

| Variable | Default | Meaning |
|----------|---------|---------|
| `HEALTHCHECK_INTERVAL` | `30s` | how often the worker ticks |
| `TRADEFLOW_RECOVERY_GRACE` | `3m` | how long a row must sit in an intermediate tradeflow state before recovery will touch it (see *Grace period*) |

---

## How it runs

`RunPeriodic(ctx, interval)` is launched as a goroutine. It is a simple ticker loop
that calls `Run(ctx)` immediately and then on every interval until `ctx` is
cancelled:

```
RunPeriodic ── every interval ──▶ Run ──▶ GetIncompleteTransactions
                                            └─ for each tx: verify(tx)
```

`Run` fetches all transactions that have not reached the terminal
`DexPositionReleased` and calls `verify` on each. A failure on one transaction is
**logged and skipped** — one bad row never blocks the others.

`verify` is a `switch` on `tx.Status` that dispatches to a per-status handler:

| Status | Handler | Action |
|--------|---------|--------|
| `FiatToCexInitiated` | `verifyFiatToCexInitiated` | ask openclaw if the fiat wire to the CEX was sent → `FiatToCexArrived` |
| `FiatToCexArrived` | (no-op) | handed to the tradeflow orchestrator |
| **`CexTradeflowStarted`** | `verifyCexTradeflowStarted` | **recovery** — resume from the hedge step, or park for review |
| **`HedgeFailedNeedsReview`** | `verifyHedgeFailedNeedsReview` | **recovery** — re-check the venue; advance if the hedge is actually open |
| **`CexTradeflowNeedsReview`** | (no-op) | alert already fired; parked for a human |
| **`CexTradeSuccessful`** | `verifyCexTradeSuccessful` | **recovery** — retry the withdrawal idempotently |
| `CexCryptoWithdrawalInitiated` | (no-op) | owned by the `dexrelay` worker |
| `CexCryptoWithdrawalArrived` | (no-op) | owned by the `dexrelay` worker |
| `DexPositionOpen` | `verifyDexPositionOpen` | DEX: counterparty joined → `DexPositionTaken` |
| `DexPositionTaken` | `verifyDexPositionTaken` | DEX: buyer marked paid → `DexFiatHasArrived` |
| `DexFiatHasArrived` | `verifyDexFiatHasArrived` | bank: confirm fiat arrived (gates BTC release) |
| `DexPositionReleased` | `verifyDexPositionReleased` | DEX: BTC released → mark complete |

An unknown status returns an error (logged, row skipped).

---

## Tradeflow recovery (the three bold rows above)

`TradeFlow.Run` performs the CEX buy leg **synchronously inside the HTTP request**
(spot buy → open hedge → withdraw), persisting the row after each step. If a step
fails or the process restarts mid-cycle, the row is simply left where it stopped.
healthcheck is what comes back to finish or flag it.

The handlers do **not** re-implement any CEX step; they delegate to recovery
entrypoints on `tradeflow` (`internal/tradeflow/recovery.go`) so the idempotent
withdrawal key, the guarded hedge open, and the persistence helpers stay in one
place. healthcheck is the *scheduler*; tradeflow is the *executor*.

| Stuck state | What it means | Recovery |
|-------------|---------------|----------|
| `CexTradeflowStarted` | row created; spot-buy outcome unknown | if the spot fill was recorded → resume from the hedge step; if unknown → move to `CexTradeflowNeedsReview` (never auto-buys — `MarketBuyBTC` has no idempotency key) |
| `CexTradeSuccessful` | spot + hedge done, withdrawal never sent / failed | retry the withdrawal, guarded by a history match so it can't double-withdraw |
| `HedgeFailedNeedsReview` | spot bought, hedge failed → holding unhedged BTC | re-query the venue; if the hedge is now open → `CexTradeSuccessful`, else leave for manual review |

### Grace period — why recovery waits

Because `Run` is synchronous and in-flight during a request, a recovery tick could
otherwise race a run that is *about* to perform the same step (e.g. see
`CexTradeSuccessful` and fire a withdrawal while the live run is also about to).
`verifyCexTradeflowStarted` and `verifyCexTradeSuccessful` therefore skip any row
whose `UpdatedAt` is newer than `recoveryGracePeriod` (`withinGrace`). A live run
finishes in seconds, so a row stuck for minutes is almost certainly abandoned.

`HedgeFailedNeedsReview` needs no grace period — `Run` has already returned by the
time a row reaches it.

The grace period is the *primary* defense; the recovery entrypoints also carry
their own idempotency guards (withdrawal history-match, hedge live-position check)
as a deeper net.

---

## Ownership — no conflict with other workers

After recovery was added, the three workers act on **disjoint** statuses, so they
never fight over a row:

- **tradeflow (live request):** drives the healthy path
  `CexTradeflowStarted → CexTradeSuccessful → CexCryptoWithdrawalInitiated`.
- **healthcheck (this package):** owns the tradeflow failure/stuck states and
  re-drives `CexTradeSuccessful → CexCryptoWithdrawalInitiated` when the live run
  didn't, plus the DEX-side lifecycle states.
- **dexrelay:** owns `CexCryptoWithdrawalInitiated` / `CexCryptoWithdrawalArrived`
  (withdrawal confirmation + LND→DEX relay).

---

## Status: startup currently disabled

The periodic workers (`healthcheck` and `dexrelay`) take automatic, money-moving
corrective actions. While the tradeflow/recovery logic is being validated, their
startup goroutines in `main.go` are **commented out** — the checker is fully
constructed and wired, it just does not tick:

```go
// go hc.RunPeriodic(ctx, healthcheckInterval)
// go dexRelayWorker.RunPeriodic(ctx, healthcheckInterval)
```

Re-enable by uncommenting those two lines (and removing the adjacent `_ =`
keep-alive references).
