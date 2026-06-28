package dexaccount

import (
	"context"
	"strings"
	"testing"

	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
)

// TestReleaseDispatchErrors covers the venue-dispatch guard rails without touching the
// network: missing contract id, unsupported venues (incl. the route/enum-only bisq2 and
// peachbitcoin that have no client), and unconfigured clients.
func TestReleaseDispatchErrors(t *testing.T) {
	r := NewReleaser(nil, nil, nil, nil, HodlHodlEscrowConfig{})
	ctx := context.Background()

	cases := []struct {
		name     string
		in       ReleaseInput
		wantSnip string
	}{
		{"empty contract id", ReleaseInput{Exchange: "hodlhodl"}, "contract_id is required"},
		{"unsupported bisq2", ReleaseInput{Exchange: "bisq2", ContractID: "x"}, "not supported"},
		{"unsupported peach", ReleaseInput{Exchange: "peachbitcoin", ContractID: "x"}, "not supported"},
		{"unknown venue", ReleaseInput{Exchange: "nope", ContractID: "x"}, "not supported"},
		{"hodlhodl nil client", ReleaseInput{Exchange: "hodlhodl", ContractID: "x"}, "not configured"},
		{"bisq nil client", ReleaseInput{Exchange: "bisq", ContractID: "x"}, "not configured"},
		{"robosats nil client", ReleaseInput{Exchange: "robosats", ContractID: "x"}, "not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Release(ctx, tc.in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSnip) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSnip)
			}
		})
	}
}

// TestReleaseHodlHodlRequiresEscrowSecrets confirms a configured HodlHodl client still
// refuses to release without the escrow seed/password — and does so before any network
// call (the secrets check precedes GetContract).
func TestReleaseHodlHodlRequiresEscrowSecrets(t *testing.T) {
	r := NewReleaser(nil, nil, hodlhodl.NewClient("", "k"), nil, HodlHodlEscrowConfig{})
	_, err := r.Release(context.Background(), ReleaseInput{Exchange: "hodlhodl", ContractID: "C1"})
	if err == nil || !strings.Contains(err.Error(), "escrow seed/payment password not configured") {
		t.Fatalf("want escrow-not-configured error, got %v", err)
	}
}
