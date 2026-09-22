package pretrade

import "strings"

// MatchWhitelistedLNDAddress returns the first local-wallet address that is
// present in the CEX withdrawal whitelist.
func MatchWhitelistedLNDAddress(walletAddresses, cexWhitelistAddresses []string) string {
	whitelisted := make(map[string]struct{}, len(cexWhitelistAddresses))
	for _, address := range cexWhitelistAddresses {
		if address = strings.TrimSpace(address); address != "" {
			whitelisted[address] = struct{}{}
		}
	}
	for _, address := range walletAddresses {
		address = strings.TrimSpace(address)
		if _, ok := whitelisted[address]; ok {
			return address
		}
	}
	return ""
}

// AddressIsWhitelisted reports whether address is present in the CEX whitelist.
func AddressIsWhitelisted(address string, cexWhitelistAddresses []string) bool {
	address = strings.TrimSpace(address)
	if address == "" {
		return false
	}
	for _, candidate := range cexWhitelistAddresses {
		if strings.EqualFold(address, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
