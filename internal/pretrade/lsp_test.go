package pretrade

import (
	"context"
	"testing"
	"time"

	"dexMmController/internal/lsp"
	"dexMmController/internal/swap"
)

func TestLSPBidUsesExistingOutboundThenReportsSwapShortfall(t *testing.T) {
	c := &BidChecker{}
	c.SetLSPStatus(LSPStatusSourceFunc(func(context.Context) (lsp.Status, error) {
		return lsp.Status{LSPHealthy: true, UsableOutboundSat: 700, LSPInboundSat: 500, MaxSwapFeeSat: 100, MaxSwapFeePPM: 100000}, nil
	}))
	c.SetSwapQuoteSource(SwapQuoteSourceFunc(func(context.Context, int64) (swap.Quote, error) {
		return swap.Quote{LightningOutputSat: 400, LockupAmountSat: 430, ServiceFeeSat: 20, MinerFeeSat: 10, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}))
	c.SetBTCBalance(BTCBalanceSourceFunc(func(context.Context) (float64, error) { return 0.01, nil }))
	r := Report{OK: true}
	err := c.checkLSPAndLiquidity(context.Background(), "bid", Params{RequiredBTCLiquidity: .00001, RoutingFeeAllowanceSat: 100}, &r)
	if err != nil || !r.SwapRequired || r.SwapShortfallSat != 400 {
		t.Fatalf("err=%v report=%+v", err, r)
	}
}

func TestLSPAskRequiresRegisteredChannelAndNodeInbound(t *testing.T) {
	c := &BidChecker{}
	c.SetLSPStatus(LSPStatusSourceFunc(func(context.Context) (lsp.Status, error) {
		return lsp.Status{LSPHealthy: false, Reason: "registered LSP channel is absent or inactive"}, nil
	}))
	r := Report{OK: true}
	if err := c.checkLSPAndLiquidity(context.Background(), "ask", Params{RequiredBTCLiquidity: .00001}, &r); err == nil || r.BlockingReason == "" {
		t.Fatalf("want LSP block, got %+v", r)
	}
}
