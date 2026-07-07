package obs

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	lndCacheTTL     = 10 * time.Second
	lndQueryTimeout = 5 * time.Second
)

// LNDStats is one sample of wallet-level LND state, fetched on scrape.
type LNDStats struct {
	// Up reports whether the wallet client is wired and healthy.
	Up bool
	// ChannelsActive / ChannelsInactive count channels by active flag.
	ChannelsActive, ChannelsInactive float64
	// ConfirmedSat / UnconfirmedSat are the on-chain balances in satoshis.
	ConfirmedSat, UnconfirmedSat float64
	// LocalSat / RemoteSat are the channel balances in satoshis.
	LocalSat, RemoteSat float64
}

// LNDStatsFunc fetches a sample from the live LND node. It runs under the
// collector's timeout and its result is cached briefly across scrapes.
//
// Defined as a closure (wired by main once the wallet client exists) rather
// than an interface on *wallethub.Client: wallethub imports the CEX connectors,
// which record into this package, so obs importing wallethub back would cycle.
type LNDStatsFunc func(ctx context.Context) LNDStats

// lndCollector is registered by Register; the source arrives later via
// SetLNDStatsSource once the wallet is initialized (possibly on demand from
// the UI).
var lndCollector = &lndMetrics{
	up: prometheus.NewDesc("dex_lnd_wallet_up",
		"1 when the LND wallet client is wired and its health check passes, else 0.", nil, nil),
	channels: prometheus.NewDesc("dex_lnd_channels",
		"Number of LND channels by active state.", []string{"active"}, nil),
	onchain: prometheus.NewDesc("dex_lnd_onchain_balance_sat",
		"LND on-chain wallet balance in satoshis by confirmation state.", []string{"state"}, nil),
	channelBal: prometheus.NewDesc("dex_lnd_channel_balance_sat",
		"LND channel balance in satoshis by side (local/remote).", []string{"side"}, nil),
}

// SetLNDStatsSource wires the live wallet stats fetcher into the collector.
// Safe to call again after a wallet re-init.
func SetLNDStatsSource(fn LNDStatsFunc) {
	lndCollector.mu.Lock()
	defer lndCollector.mu.Unlock()
	lndCollector.fetch = fn
	lndCollector.cachedAt = time.Time{}
}

type lndMetrics struct {
	mu       sync.Mutex
	fetch    LNDStatsFunc
	cached   *LNDStats
	cachedAt time.Time

	up         *prometheus.Desc
	channels   *prometheus.Desc
	onchain    *prometheus.Desc
	channelBal *prometheus.Desc
}

func (m *lndMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.up
	ch <- m.channels
	ch <- m.onchain
	ch <- m.channelBal
}

func (m *lndMetrics) Collect(ch chan<- prometheus.Metric) {
	snap := m.snapshot()
	if !snap.Up {
		ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(m.channels, prometheus.GaugeValue, snap.ChannelsActive, "true")
	ch <- prometheus.MustNewConstMetric(m.channels, prometheus.GaugeValue, snap.ChannelsInactive, "false")
	ch <- prometheus.MustNewConstMetric(m.onchain, prometheus.GaugeValue, snap.ConfirmedSat, "confirmed")
	ch <- prometheus.MustNewConstMetric(m.onchain, prometheus.GaugeValue, snap.UnconfirmedSat, "unconfirmed")
	ch <- prometheus.MustNewConstMetric(m.channelBal, prometheus.GaugeValue, snap.LocalSat, "local")
	ch <- prometheus.MustNewConstMetric(m.channelBal, prometheus.GaugeValue, snap.RemoteSat, "remote")
}

func (m *lndMetrics) snapshot() LNDStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != nil && time.Since(m.cachedAt) < lndCacheTTL {
		return *m.cached
	}
	var snap LNDStats
	if m.fetch != nil {
		ctx, cancel := context.WithTimeout(context.Background(), lndQueryTimeout)
		defer cancel()
		snap = m.fetch(ctx)
	}
	m.cached = &snap
	m.cachedAt = time.Now()
	return snap
}
