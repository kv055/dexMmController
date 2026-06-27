package dexaccount

import (
	"context"
	"fmt"
	"strings"

	"dexMmController/internal/exchangeconnectors/dex/bisq"
)

// DirectResolver resolves the withdrawal destination for direct-routed DEXes, where the
// CEX withdraws straight into a DEX wallet we control (no LND hub hop). It satisfies
// tradeflow.DirectDestinationResolver.
type DirectResolver struct {
	bisq *bisq.Client
}

// NewDirectResolver creates a DirectResolver backed by the configured DEX clients.
func NewDirectResolver(bisqClient *bisq.Client) *DirectResolver {
	return &DirectResolver{bisq: bisqClient}
}

// DirectDestination returns the on-chain address the CEX should withdraw to for a
// direct-routed DEX. Currently only bisq v1 uses direct routing — the CEX funds bisq's
// integrated wallet, which then funds the per-trade multisig itself.
func (r *DirectResolver) DirectDestination(ctx context.Context, dexSlug string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(dexSlug)) {
	case "bisq":
		if r == nil || r.bisq == nil {
			return "", fmt.Errorf("bisq client not configured for direct routing")
		}
		return r.bisq.FundingAddress(ctx)
	default:
		return "", fmt.Errorf("no direct-route wallet resolver for dex %q", dexSlug)
	}
}
