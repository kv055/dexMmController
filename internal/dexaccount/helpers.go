package dexaccount

import (
	"fmt"
	"math"
	"strings"
)

// BTCToSats converts a BTC amount to satoshis.
func BTCToSats(btc float64) int64 {
	if btc <= 0 {
		return 0
	}
	return int64(math.Round(btc * 1e8))
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
