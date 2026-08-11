package tradeflow

import (
	"context"
	"fmt"
	"math"
	"time"

	"dexMmController/internal/lsp"
	"dexMmController/internal/pretrade"
	"dexMmController/internal/swap"
)

type confirmedBalanceSource interface {
	ConfirmedOnChainBTC(context.Context) (float64, error)
}

// SwapExecutor is the only automatic swap entry point. It accepts a shortfall
// from a trade's read-only preparation report; it cannot perform generic rebalancing.
type SwapExecutor struct {
	Coordinator *swap.Coordinator
	Balance     confirmedBalanceSource
	Status      interface {
		Status(context.Context) (lsp.Status, error)
	}
}

func (e SwapExecutor) EnsureOutbound(ctx context.Context, reservationID string, tradeID uint, report *pretrade.Report) error {
	if report == nil || !report.SwapRequired {
		return nil
	}
	if e.Coordinator == nil || e.Balance == nil {
		return fmt.Errorf("submarine swap executor is not configured")
	}
	btc, err := e.Balance.ConfirmedOnChainBTC(ctx)
	if err != nil {
		return fmt.Errorf("read confirmed LND on-chain balance: %w", err)
	}
	onchain := int64(math.Floor(btc * 1e8))
	record, err := e.Coordinator.Execute(ctx, reservationID, tradeID, report.SwapShortfallSat, report.RequiredLightningSat, report.UsableOutboundSat, report.UsableInboundSat, onchain)
	if err != nil {
		return err
	}
	if record.State != swap.StateSettled {
		return fmt.Errorf("submarine swap ended in %s", record.State)
	}
	if e.Status == nil {
		return fmt.Errorf("post-swap LSP liquidity verification is not configured")
	}
	verified, err := e.Status.Status(ctx)
	if err != nil {
		return fmt.Errorf("verify post-swap liquidity: %w", err)
	}
	if !verified.LSPHealthy || verified.UsableOutboundSat < report.RequiredLightningSat {
		return fmt.Errorf("usable outbound liquidity after swap is %d sats; required %d sats", verified.UsableOutboundSat, report.RequiredLightningSat)
	}
	return nil
}

// PrepareOutbound is used by the hedgefull relay only after the CEX withdrawal
// is confirmed. It reserves the full payment budget, swaps only the current
// shortfall, and verifies the resulting usable outbound balance before Take.
func (e SwapExecutor) PrepareOutbound(ctx context.Context, reservationID string, tradeID uint, paymentSat, feeAllowanceSat int64) error {
	if e.Status == nil {
		return fmt.Errorf("LSP liquidity status is not configured")
	}
	status, err := e.Status.Status(ctx)
	if err != nil {
		return err
	}
	if !status.LSPHealthy {
		return fmt.Errorf("registered LSP channel unavailable: %s", status.Reason)
	}
	required := paymentSat + feeAllowanceSat
	if paymentSat <= 0 || feeAllowanceSat <= 0 || required <= paymentSat {
		return fmt.Errorf("invalid Lightning payment or routing-fee allowance")
	}
	if status.UsableOutboundSat >= required {
		if e.Coordinator == nil {
			return fmt.Errorf("liquidity reservation coordinator is not configured")
		}
		return e.Coordinator.ReserveOutbound(ctx, reservationID, tradeID, required, status.UsableOutboundSat, time.Now().Add(15*time.Minute))
	}
	report := &pretrade.Report{
		SwapRequired:         true,
		RequiredLightningSat: required,
		SwapShortfallSat:     required - status.UsableOutboundSat,
		UsableOutboundSat:    status.UsableOutboundSat,
		UsableInboundSat:     status.UsableInboundSat,
	}
	if status.LSPInboundSat < report.SwapShortfallSat {
		return fmt.Errorf("insufficient registered LSP inbound liquidity: available %d sats, required %d sats", status.LSPInboundSat, report.SwapShortfallSat)
	}
	if err = e.EnsureOutbound(ctx, reservationID, tradeID, report); err != nil {
		return err
	}
	return nil
}
func (e SwapExecutor) Release(ctx context.Context, id string) error {
	if e.Coordinator == nil {
		return nil
	}
	return e.Coordinator.Release(ctx, id)
}
