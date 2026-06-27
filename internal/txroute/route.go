// Package txroute mirrors the bid-side DEX routing matrix from
// commons/tradecost/txrouting for use inside dexMmController.
//
// dexMmController is built and deployed as its own module/submodule and cannot
// import the (separate, unpublished) commons module, so the small routing matrix is
// duplicated here. Keep this in sync with
// commons/tradecost/txrouting/dex_routing_profiles.go — the source of truth.
//
// Only the bid side is mirrored: the CEX→DEX second leg always sells BTC on the DEX
// (we bought it on the CEX), so it is bid-only. The ask side is always Direct (BTC
// arrives at our own CEX deposit) and is not needed here.
package txroute

import "strings"

// Method is the on-chain routing method for the BTC leg of a taken DEX offer.
type Method string

const (
	// Direct: the CEX withdraws straight into a wallet we control (bisq v1's
	// integrated wallet), which funds the trade itself — no LND hub hop.
	Direct Method = "onchain_direct"
	// Hub: CEX → our LND hub → forward on-chain to the counterparty's per-trade
	// address (bisq2/hodlhodl/peach).
	Hub Method = "onchain_hub"
	// Swap: CEX → our LND hub on-chain, then the BTC leaves over Lightning by paying
	// the offer's hold invoice (robosats).
	Swap Method = "onchain_swap"
)

// bidMethods maps a DEX slug to its bid-side routing method.
var bidMethods = map[string]Method{
	"bisq":         Direct,
	"bisq2":        Hub,
	"hodlhodl":     Hub,
	"robosats":     Swap,
	"peachbitcoin": Hub,
}

// BidMethod returns the bid-side routing method for a DEX slug, defaulting to Hub
// (the safe choice for an unknown, per-trade-changing destination).
func BidMethod(dexSlug string) Method {
	if m, ok := bidMethods[strings.ToLower(strings.TrimSpace(dexSlug))]; ok {
		return m
	}
	return Hub
}

// NeedsHub reports whether the route funds through our LND hub (Hub or Swap), i.e. the
// CEX withdraws to our LND wallet and the second leg watches that hub address.
func (m Method) NeedsHub() bool { return m == Hub || m == Swap }
