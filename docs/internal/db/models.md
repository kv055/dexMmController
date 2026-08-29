# Transaction lifecycle model

Source: `internal/db/models.go`

`Transaction.Status` is a required `TransactionStatus`. `StatusReason` is an
optional operator-facing explanation for review and failure states. Use
`Transaction.SetStatus(status, reason)` when changing status so a successful
transition clears an obsolete reason.

`TransactionComplete` is the authoritative work-queue boundary. A row may have
status `DexPositionReleased` and remain incomplete while independent venue and
chain verification is pending. Conversely, `HedgelessPreparationFailed` is a
terminal audit row and is complete because no funds were proven to have moved.

## Canonical statuses

| Status | Meaning and owner |
| --- | --- |
| `TransactionNeedsReview` | A legacy or otherwise unclassifiable row that needs operator reconciliation. |
| `CexTradeflowStarted` | The hedgefull CEX flow has a durable intent row and may be resumed after the recovery grace period. |
| `HedgeFailedNeedsReview` | The CEX trade succeeded, but hedge placement or confirmation is ambiguous. |
| `CexTradeflowNeedsReview` | A broader hedgefull recovery ambiguity that must be reconciled manually. |
| `HedgelessTradeStarted` | The hedgeless BTC-pool reservation exists; no durable DEX funding receipt has been recorded yet. |
| `HedgelessPreparationFailed` | Terminal pre-funding failure with no evidence that money moved. The pool reservation is restored and the row is complete. |
| `LiquidityPreparationNeedsReview` | Submarine-swap or outbound-liquidity preparation may have moved funds; automation must retain the reservation and stop. |
| `HedgelessFundingInitiated` | A DEX funding action is in progress but the final receipt/take has not yet been durably recorded. |
| `HedgelessFundingNeedsReview` | Hedgeless funding is ambiguous before a durable DEX take is known. |
| `DexTakenFundingNeedsReview` | The DEX position was taken, but its funding, invoice, or durable receipt is ambiguous. |
| `CexTradeSuccessful` | The CEX spot leg and required hedge state are durably recorded. |
| `CexCryptoWithdrawalInitiated` | The CEX-to-LND withdrawal was submitted and is awaiting confirmation. |
| `CexCryptoWithdrawalArrived` | The withdrawn BTC arrived; the DEX relay may now take and fund the position. |
| `DexPositionTaken` | The DEX offer was successfully taken and its required funding receipt was durably stored. |
| `DexFiatHasArrived` | Trusted bank-side evidence or explicit operator confirmation matched the expected fiat payment. DEX paid/sent flags cannot set this status. |
| `DexPositionReleased` | BTC release was submitted. `TransactionComplete=false` keeps the row eligible for independent completion verification. |

The removed values `FiatToCexInitiated`, `FiatToCexArrived`,
`CexTradeSucessfull`, and `DexPositionOpen` are migration-only inputs and must
not be emitted by runtime code.

## Related state

Hedgeless pool restoration has its own `BTCPoolRefillStatus` lifecycle because
it can continue after the DEX position is taken. A transaction status describes
trade safety and ownership; refill status describes whether shared collateral
is available for another trade.
