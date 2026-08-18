// Package lsp owns the operator-selected, long-lived LSP channel policy.
// It deliberately has no channel-open, channel-close, or renewal operation.
package lsp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	lnd "dexMmController/internal/wallethub"
)

// Config is the singleton operator configuration for a manually acquired LSP
// channel. Both identifiers are required to prevent a same-peer replacement
// channel from silently satisfying the health requirement.
type Config struct {
	ChannelPoint  string    `json:"channel_point"`
	RemotePubkey  string    `json:"remote_pubkey"`
	ProviderLabel string    `json:"provider_label"`
	Enabled       bool      `json:"enabled"`
	HeadroomSat   int64     `json:"headroom_sat"`
	MaxSwapFeeSat int64     `json:"max_swap_fee_sat"`
	MaxSwapFeePPM int64     `json:"max_swap_fee_ppm"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.ChannelPoint) == "" || strings.TrimSpace(c.RemotePubkey) == "" {
		return fmt.Errorf("channel_point and remote_pubkey are required when LSP channel is enabled")
	}
	if c.HeadroomSat < 0 || c.MaxSwapFeeSat < 0 || c.MaxSwapFeePPM < 0 {
		return fmt.Errorf("LSP amounts cannot be negative")
	}
	return nil
}

// Status separates the specifically registered channel health from node-wide
// usable balances. Unrelated active channels may add node liquidity but never
// make LSPHealthy true.
type Status struct {
	Configured        bool   `json:"configured"`
	Enabled           bool   `json:"enabled"`
	LSPHealthy        bool   `json:"lsp_healthy"`
	Reason            string `json:"reason,omitempty"`
	ChannelPoint      string `json:"channel_point,omitempty"`
	RemotePubkey      string `json:"remote_pubkey,omitempty"`
	ProviderLabel     string `json:"provider_label,omitempty"`
	LSPInboundSat     int64  `json:"lsp_inbound_sat"`
	LSPOutboundSat    int64  `json:"lsp_outbound_sat"`
	UsableInboundSat  int64  `json:"usable_inbound_sat"`
	UsableOutboundSat int64  `json:"usable_outbound_sat"`
	HeadroomSat       int64  `json:"headroom_sat"`
	MaxSwapFeeSat     int64  `json:"max_swap_fee_sat"`
	MaxSwapFeePPM     int64  `json:"max_swap_fee_ppm"`
}

// Inspect matches the configured point and pubkey exactly (ignoring case),
// then calculates balances from active channels only. LND reserve, commitment
// fee and pending HTLC amounts are conservatively excluded.
func Inspect(cfg Config, channels []lnd.Channel) Status {
	s := Status{Enabled: cfg.Enabled, Configured: strings.TrimSpace(cfg.ChannelPoint) != "" && strings.TrimSpace(cfg.RemotePubkey) != "", ChannelPoint: cfg.ChannelPoint, RemotePubkey: cfg.RemotePubkey, ProviderLabel: cfg.ProviderLabel, HeadroomSat: cfg.HeadroomSat, MaxSwapFeeSat: cfg.MaxSwapFeeSat, MaxSwapFeePPM: cfg.MaxSwapFeePPM}
	if !cfg.Enabled {
		if !s.Configured {
			s.Reason = "No LSP channel configured"
			return s
		}
		s.Reason = "LSP channel is disabled"
		return s
	}
	if !s.Configured {
		s.Reason = "LSP channel is not configured"
		return s
	}
	for _, ch := range channels {
		if !ch.Active {
			continue
		}
		local, remote := usable(ch, cfg.HeadroomSat)
		s.UsableOutboundSat += local
		s.UsableInboundSat += remote
		if same(ch.ChannelPoint, cfg.ChannelPoint) && same(ch.RemotePubkey, cfg.RemotePubkey) {
			s.LSPHealthy = true
			s.LSPOutboundSat = local
			s.LSPInboundSat = remote
		}
	}
	s.UsableOutboundSat = ApplyHeadroom(s.UsableOutboundSat, cfg.HeadroomSat)
	s.UsableInboundSat = ApplyHeadroom(s.UsableInboundSat, cfg.HeadroomSat)
	s.LSPOutboundSat = ApplyHeadroom(s.LSPOutboundSat, cfg.HeadroomSat)
	s.LSPInboundSat = ApplyHeadroom(s.LSPInboundSat, cfg.HeadroomSat)
	if !s.LSPHealthy {
		s.Reason = "registered LSP channel is absent or inactive"
	}
	return s
}

func usable(ch lnd.Channel, headroom int64) (int64, int64) {
	local := sat(ch.LocalBalance) - sat(ch.LocalChanReserveSat) - sat(ch.CommitFee) - pending(ch)
	remote := sat(ch.RemoteBalance) - sat(ch.RemoteChanReserveSat) - pending(ch)
	if local < 0 {
		local = 0
	}
	if remote < 0 {
		remote = 0
	}
	// Headroom is a node-level safety amount. Applying it once per direction is
	// conservative without multiplying it by the number of channels.
	return local, remote
}

func pending(ch lnd.Channel) int64 {
	var total int64
	for _, h := range ch.PendingHTLCs {
		total += sat(h.Amount)
	}
	return total
}
func sat(v string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if n < 0 {
		return 0
	}
	return n
}
func same(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// ApplyHeadroom removes configured safety headroom once from a node-wide total.
func ApplyHeadroom(sats, headroom int64) int64 {
	if sats <= headroom {
		return 0
	}
	return sats - headroom
}
