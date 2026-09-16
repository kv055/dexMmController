package bisq2

import (
	"strings"
	"unicode"
)

const (
	// BitcoinRailMainChain is native on-chain BTC (Bisq Easy BitcoinPaymentRail.MAIN_CHAIN).
	BitcoinRailMainChain = "MAIN_CHAIN"
	// BitcoinRailLN is Lightning (Bisq Easy BitcoinPaymentRail.LN).
	BitcoinRailLN = "LN"
)

var canonicalFiatRails = map[string]string{
	"SEPA":            "SEPA",
	"SEPA INSTANT":    "SEPA_INSTANT",
	"NATIONAL BANK":   "NATIONAL_BANK",
	"REVOLUT":         "REVOLUT",
	"WISE":            "WISE",
	"N26":             "MONEY_BEAM",
	"MONEYBEAM":       "MONEY_BEAM",
	"MONEY BEAM":      "MONEY_BEAM",
	"ZELLE":           "ZELLE",
	"STRIKE":          "STRIKE",
	"FASTER PAYMENTS": "FASTER_PAYMENTS",
	"PIX":             "PIX",
	"SWIFT":           "SWIFT",
	"PAYPAL":          "CUSTOM",
}

// FiatRailName maps a catalog/canonical payment method onto a Bisq Easy FiatPaymentRail
// enum name. Unknown labels become UPPER_SNAKE, which the daemon treats as a custom
// rail when they are not in the enum.
func FiatRailName(canonical string) string {
	trimmed := strings.TrimSpace(canonical)
	if trimmed == "" {
		return ""
	}
	key := strings.ToUpper(strings.Join(strings.Fields(strings.ReplaceAll(trimmed, "_", " ")), " "))
	if rail, ok := canonicalFiatRails[key]; ok {
		return rail
	}
	return toUpperSnake(trimmed)
}

func toUpperSnake(s string) string {
	var b strings.Builder
	prevUnderscore := true
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			prevUnderscore = false
		case !prevUnderscore:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// PickBitcoinRail prefers MAIN_CHAIN (our hub on-chain send) when the offer
// supports it, otherwise LN, otherwise the first advertised rail.
func PickBitcoinRail(rails []string) string {
	var first, ln string
	for _, raw := range rails {
		rail := strings.ToUpper(strings.TrimSpace(raw))
		switch rail {
		case BitcoinRailMainChain:
			return BitcoinRailMainChain
		case BitcoinRailLN, "LIGHTNING":
			if ln == "" {
				ln = BitcoinRailLN
			}
		default:
			if first == "" && rail != "" {
				first = rail
			}
		}
	}
	if ln != "" {
		return ln
	}
	if first != "" {
		return first
	}
	return BitcoinRailMainChain
}

// MatchFiatRail returns the offer rail that matches our selected method, or the
// sole offer rail when the offer only advertises one.
func MatchFiatRail(selected string, offerRails []string) string {
	want := FiatRailName(selected)
	normalized := make([]string, 0, len(offerRails))
	for _, raw := range offerRails {
		rail := strings.TrimSpace(raw)
		if rail == "" {
			continue
		}
		normalized = append(normalized, rail)
		if want != "" && strings.EqualFold(rail, want) {
			return rail
		}
		if want != "" && strings.EqualFold(FiatRailName(rail), want) {
			return rail
		}
	}
	if want != "" {
		return want
	}
	if len(normalized) == 1 {
		return normalized[0]
	}
	return ""
}

// ClassifyBitcoinPaymentData splits a Bisq Easy bitcoinPaymentData value into an
// on-chain address or a Lightning invoice.
func ClassifyBitcoinPaymentData(data string) (address, invoice string) {
	data = strings.TrimSpace(data)
	if data == "" {
		return "", ""
	}
	lower := strings.ToLower(data)
	if strings.HasPrefix(lower, "ln") {
		return "", data
	}
	return data, ""
}
