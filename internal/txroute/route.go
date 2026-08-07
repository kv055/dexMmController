// Package txroute mirrors the DEX routing matrix from
// commons/tradecost/txrouting for use inside dexMmController.
//
// dexMmController is built and deployed as its own module/submodule and cannot
// import the (separate, unpublished) commons module, so the small routing matrix is
// duplicated here. Keep this in sync with
// commons/tradecost/txrouting/dex_routing_profiles.go — the source of truth.
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
	// Swap: Lightning BTC leg (Swap_main2lightning). Bid pays a hold invoice from
	// local channel outbound; ask receives over Lightning into inbound capacity.
	Swap Method = "onchain_swap"
)

// Side is the DEX offer side from our perspective.
type Side string

const (
	SideBid Side = "bid" // we sell BTC on the DEX
	SideAsk Side = "ask" // we buy BTC on the DEX
)

// dexMethods maps a DEX slug to bid/ask routing methods (mirrors commons).
var dexMethods = map[string]struct{ Bid, Ask Method }{
	"bisq":         {Bid: Direct, Ask: Direct},
	"bisq2":        {Bid: Hub, Ask: Direct},
	"hodlhodl":     {Bid: Hub, Ask: Direct},
	"robosats":     {Bid: Swap, Ask: Swap},
	"peachbitcoin": {Bid: Hub, Ask: Direct},
}

// NormalizeSide returns bid or ask; anything other than ask defaults to bid.
func NormalizeSide(raw string) Side {
	if strings.EqualFold(strings.TrimSpace(raw), string(SideAsk)) {
		return SideAsk
	}
	return SideBid
}

// MethodFor returns the routing method for a DEX slug and offer side.
// Unknown DEXes default to Hub on bid (changing destination) and Direct on ask
// (our own CEX deposit).
func MethodFor(dexSlug string, side Side) Method {
	key := strings.ToLower(strings.TrimSpace(dexSlug))
	p, ok := dexMethods[key]
	if !ok {
		if side == SideAsk {
			return Direct
		}
		return Hub
	}
	if side == SideAsk {
		return p.Ask
	}
	return p.Bid
}

// BidMethod returns the bid-side routing method for a DEX slug.
func BidMethod(dexSlug string) Method {
	return MethodFor(dexSlug, SideBid)
}

// HasLightningLeg reports whether the route includes a Lightning payment leg
// (routing profile Swap_main2lightning).
func (m Method) HasLightningLeg() bool { return m == Swap }

// NeedsHub reports whether the route funds through our LND hub (Hub or Swap), i.e. the
// CEX withdraws to our LND wallet and the second leg watches that hub address.
func (m Method) NeedsHub() bool { return m == Hub || m == Swap }
