package dexaccount

import (
	"context"
	"fmt"
	"strings"

	"dexTakerController/internal/exchangeconnectors/dex/bisq"
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

// DirectBalanceBTC returns the confirmed balance held at a direct-route DEX
// funding address. Hedgeless Bisq uses it to wait for the LND transfer before
// asking the Bisq daemon to take the offer.
func (r *DirectResolver) DirectBalanceBTC(ctx context.Context, dexSlug, address string) (float64, error) {
	if strings.ToLower(strings.TrimSpace(dexSlug)) != "bisq" {
		return 0, fmt.Errorf("no direct-route balance resolver for dex %q", dexSlug)
	}
	if r == nil || r.bisq == nil {
		return 0, fmt.Errorf("bisq client not configured for direct routing")
	}
	rows, err := r.bisq.GetFundingAddresses(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Address), strings.TrimSpace(address)) {
			return float64(row.BalanceSats) / 1e8, nil
		}
	}
	return 0, nil
}
