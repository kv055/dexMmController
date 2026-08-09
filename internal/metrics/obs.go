// Package obs holds dexMmController's domain Prometheus instruments.
//
// Instruments are package-level so deep call sites (tradeflow, connectors,
// watchers) can record events without threading a registry through every
// constructor. main registers them onto the commons service registry via
// Register; until then they exist but are simply never scraped, which keeps
// tests dependency-free.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// OrdersPlaced counts trade-leg submissions against a venue. kind is
	// "spot", "hedge" or "withdrawal"; result is "ok" or "error".
	OrdersPlaced = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dex_orders_placed_total",
			Help: "Trade-leg submissions to a CEX venue by kind (spot/hedge/withdrawal) and result.",
		},
		[]string{"venue", "kind", "result"},
	)

	// OrderDuration measures the venue round-trip of a trade-leg submission.
	OrderDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dex_order_placement_duration_seconds",
			Help:    "Duration of trade-leg submissions to a CEX venue by kind.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"venue", "kind"},
	)

	// HedgeFailures counts hedge opens that exhausted all retries after the
	// spot leg filled — the account then holds unhedged BTC pending review.
	HedgeFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dex_hedge_failures_total",
			Help: "Hedge opens that failed after the spot fill, leaving unhedged BTC (HedgeFailedNeedsReview).",
		},
		[]string{"venue"},
	)

	// TradeflowRuns counts complete trade-cycle attempts by result.
	TradeflowRuns = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dex_tradeflow_runs_total",
			Help: "Trade-cycle executions by market side and result.",
		},
		[]string{"side", "result"},
	)

	// TradeflowRecoveries counts crash-recovery resumptions by path and outcome.
	TradeflowRecoveries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dex_tradeflow_recoveries_total",
			Help: "Tradeflow recovery resumptions by path (tradeflow_started/hedge/withdrawal) and result.",
		},
		[]string{"path", "result"},
	)

	// WSReconnects counts upstream websocket/poll stream reconnect attempts.
	WSReconnects = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dex_ws_reconnects_total",
			Help: "Upstream stream reconnects by stream name (premium_offers, binance_futures_positions, ...).",
		},
		[]string{"stream"},
	)
)

// Result maps an error to the conventional "ok"/"error" result label.
func Result(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// Register attaches all package instruments (and the LND collector) to reg.
func Register(reg prometheus.Registerer) {
	reg.MustRegister(
		OrdersPlaced,
		OrderDuration,
		HedgeFailures,
		TradeflowRuns,
		TradeflowRecoveries,
		WSReconnects,
		lndCollector,
	)
}
