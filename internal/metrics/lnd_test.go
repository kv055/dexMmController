package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestLNDCollectorEmitsSyncGaugesWhenUp(t *testing.T) {
	SetLNDStatsSource(func(context.Context) LNDStats {
		return LNDStats{
			Up:            true,
			SyncedToChain: false,
			SyncedToGraph: true,
			BlockHeight:   963051,
		}
	})
	t.Cleanup(func() { SetLNDStatsSource(nil) })

	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(lndCollector); err != nil {
		t.Fatalf("register: %v", err)
	}
	gathered, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	got := map[string]float64{}
	for _, mf := range gathered {
		if len(mf.Metric) == 0 || mf.Metric[0].Gauge == nil {
			continue
		}
		got[mf.GetName()] = mf.Metric[0].GetGauge().GetValue()
	}
	for _, name := range []string{
		"dex_lnd_wallet_up",
		"dex_lnd_synced_to_chain",
		"dex_lnd_synced_to_graph",
		"dex_lnd_block_height",
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing gauge %s (have %v)", name, keys(got))
		}
	}
	if got["dex_lnd_wallet_up"] != 1 {
		t.Errorf("wallet_up=%v", got["dex_lnd_wallet_up"])
	}
	if got["dex_lnd_synced_to_chain"] != 0 {
		t.Errorf("synced_to_chain=%v", got["dex_lnd_synced_to_chain"])
	}
	if got["dex_lnd_synced_to_graph"] != 1 {
		t.Errorf("synced_to_graph=%v", got["dex_lnd_synced_to_graph"])
	}
	if got["dex_lnd_block_height"] != 963051 {
		t.Errorf("block_height=%v", got["dex_lnd_block_height"])
	}
}

func TestLNDCollectorOmitsSyncGaugesWhenDown(t *testing.T) {
	SetLNDStatsSource(func(context.Context) LNDStats {
		return LNDStats{}
	})
	t.Cleanup(func() { SetLNDStatsSource(nil) })

	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(lndCollector); err != nil {
		t.Fatalf("register: %v", err)
	}
	gathered, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]struct{}{}
	for _, mf := range gathered {
		names[mf.GetName()] = struct{}{}
	}
	if _, ok := names["dex_lnd_wallet_up"]; !ok {
		t.Fatal("expected dex_lnd_wallet_up=0 when down")
	}
	for _, forbidden := range []string{
		"dex_lnd_synced_to_chain",
		"dex_lnd_synced_to_graph",
		"dex_lnd_block_height",
	} {
		if _, ok := names[forbidden]; ok {
			t.Errorf("sync gauge %s should not emit when wallet is down", forbidden)
		}
	}
}

func keys(m map[string]float64) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}

func TestBoolGauge(t *testing.T) {
	if boolGauge(true) != 1 || boolGauge(false) != 0 {
		t.Fatal("boolGauge mapping")
	}
}
