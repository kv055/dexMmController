package dexaccount

import (
	"fmt"
	"math"
	"strings"
)

// MaxSats is the total possible satoshi supply (21M BTC). No valid amount
// can exceed it, so conversions clamp there instead of wrapping int64.
const MaxSats = int64(21_000_000) * 100_000_000

// BTCToSats converts a BTC amount to satoshis. Non-positive and NaN inputs
// yield 0; infinite or over-supply inputs clamp to MaxSats. The result never
// wraps, so callers casting to uint64 cannot mint a huge amount from a bad
// float.
func BTCToSats(btc float64) int64 {
	if math.IsNaN(btc) || btc <= 0 {
		return 0
	}
	sats := math.Round(btc * 1e8)
	if math.IsInf(sats, 0) || sats >= float64(MaxSats) {
		return MaxSats
	}
	return int64(sats)
}

// SatsToBTC converts satoshis to BTC.
func SatsToBTC(sats int64) float64 {
	if sats <= 0 {
		return 0
	}
	return math.Round(float64(sats)) / 1e8
}

// StringField extracts a trimmed string from a map[string]any.
func StringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// LooksLikeOnChainAddress returns true if addr looks like a Bitcoin on-chain address.
func LooksLikeOnChainAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	return strings.HasPrefix(addr, "bc1") || strings.HasPrefix(addr, "1") || strings.HasPrefix(addr, "3") || strings.HasPrefix(addr, "tb1")
}
