package lsp

import (
	"testing"

	lnd "dexMmController/internal/wallethub"
)

func TestInspectRequiresExactSelectedActiveChannelButCountsOtherChannels(t *testing.T) {
	cfg := Config{Enabled: true, ChannelPoint: "funding:1", RemotePubkey: "03lsp", HeadroomSat: 100}
	channels := []lnd.Channel{
		{ChannelPoint: "funding:1", RemotePubkey: "03lsp", Active: true, LocalBalance: "1000", RemoteBalance: "900", LocalChanReserveSat: "100", CommitFee: "20", PendingHTLCs: []lnd.PendingHTLC{{Amount: "30"}}},
		{ChannelPoint: "other:0", RemotePubkey: "03other", Active: true, LocalBalance: "500", RemoteBalance: "700"},
		{ChannelPoint: "inactive:0", RemotePubkey: "03inactive", Active: false, LocalBalance: "999999", RemoteBalance: "999999"},
	}
	s := Inspect(cfg, channels)
	if !s.LSPHealthy {
		t.Fatalf("selected channel should be healthy: %#v", s)
	}
	if s.LSPOutboundSat != 750 || s.LSPInboundSat != 770 {
		t.Fatalf("LSP balances = %d/%d, want 750/770", s.LSPOutboundSat, s.LSPInboundSat)
	}
	if s.UsableOutboundSat != 1250 || s.UsableInboundSat != 1470 {
		t.Fatalf("node balances = %d/%d", s.UsableOutboundSat, s.UsableInboundSat)
	}
	if ApplyHeadroom(1350, cfg.HeadroomSat) != 1250 {
		t.Fatal("headroom should be applied once to node total")
	}
}

func TestInspectUnconfiguredDoesNotReportDisabled(t *testing.T) {
	got := Inspect(Config{}, nil)
	if got.Configured || got.Enabled || got.LSPHealthy {
		t.Fatalf("unconfigured status = %#v", got)
	}
	if got.Reason != "No LSP channel configured" {
		t.Fatalf("reason = %q, want unconfigured not disabled", got.Reason)
	}
}

func TestInspectRejectsSamePeerReplacementOrInactiveChannel(t *testing.T) {
	cfg := Config{Enabled: true, ChannelPoint: "expected:0", RemotePubkey: "03lsp"}
	for _, ch := range []lnd.Channel{{ChannelPoint: "replacement:0", RemotePubkey: "03lsp", Active: true}, {ChannelPoint: "expected:0", RemotePubkey: "03lsp", Active: false}} {
		if got := Inspect(cfg, []lnd.Channel{ch}); got.LSPHealthy {
			t.Fatalf("unexpected healthy LSP status: %#v", got)
		}
	}
}
