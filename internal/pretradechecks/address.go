package pretradechecks

import "strings"

// MatchWhitelistedLNDAddress returns the first LND wallet address that appears in
// the CEX withdrawal whitelist. Comparison is case-insensitive. Returns "" when no
// match exists.
func MatchWhitelistedLNDAddress(walletAddresses, cexWhitelistAddresses []string) string {
	cexSet := make(map[string]struct{}, len(cexWhitelistAddresses))
	for _, addr := range cexWhitelistAddresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		cexSet[strings.ToLower(addr)] = struct{}{}
	}
	for _, addr := range walletAddresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := cexSet[strings.ToLower(addr)]; ok {
			return addr
		}
	}
	return ""
}
