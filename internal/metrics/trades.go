package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	tradeCacheTTL     = 5 * time.Second
	tradeQueryTimeout = 8 * time.Second
)

// TradeStatsSource is the repository subset the collector queries on scrape.
// Satisfied by *db.TransactionRepository.
type TradeStatsSource interface {
	GetPnlStats() (map[string]interface{}, error)
	CountTransactionsByStatus() (map[string]int64, error)
}

// RegisterTradeStats attaches a DB-backed collector exposing realized PnL and
// the transaction status distribution. Like unified-api's dbmetrics collector,
// it queries on scrape with a short cache so concurrent scrapes never
// double-hit the database.
func RegisterTradeStats(reg prometheus.Registerer, src TradeStatsSource) {
	reg.MustRegister(&tradeStats{
		src: src,
		pnl: prometheus.NewDesc("dex_trade_pnl_eur",
			"Realized PnL over all recorded transactions (stat: total/avg/max/min).", []string{"stat"}, nil),
		transactions: prometheus.NewDesc("dex_transactions_total",
			"Recorded transactions by lifecycle status.", []string{"status"}, nil),
	})
}

type tradeSnapshot struct {
	pnl      map[string]float64
	byStatus map[string]int64
}

type tradeStats struct {
	src TradeStatsSource

	mu       sync.Mutex
	cached   *tradeSnapshot
	cachedAt time.Time

	pnl          *prometheus.Desc
	transactions *prometheus.Desc
}

func (t *tradeStats) Describe(ch chan<- *prometheus.Desc) {
	ch <- t.pnl
	ch <- t.transactions
}

func (t *tradeStats) Collect(ch chan<- prometheus.Metric) {
	snap := t.snapshot()
	if snap == nil {
		return
	}
	for stat, v := range snap.pnl {
		ch <- prometheus.MustNewConstMetric(t.pnl, prometheus.GaugeValue, v, stat)
	}
	for status, n := range snap.byStatus {
		ch <- prometheus.MustNewConstMetric(t.transactions, prometheus.GaugeValue, float64(n), status)
	}
}

func (t *tradeStats) snapshot() *tradeSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cached != nil && time.Since(t.cachedAt) < tradeCacheTTL {
		return t.cached
	}

	snap := &tradeSnapshot{pnl: map[string]float64{}}
	if stats, err := t.src.GetPnlStats(); err == nil {
		for key, stat := range map[string]string{
			"total_pnl": "total", "avg_pnl": "avg", "max_pnl": "max", "min_pnl": "min",
		} {
			if v, ok := stats[key].(float64); ok {
				snap.pnl[stat] = v
			}
		}
	}
	if byStatus, err := t.src.CountTransactionsByStatus(); err == nil {
		snap.byStatus = byStatus
	}

	t.cached = snap
	t.cachedAt = time.Now()
	return snap
}
