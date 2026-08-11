// Package swap contains the fail-closed policy and durable-state contract for
// RoboSats submarine swaps. It has no generic rebalancing entry point.
package swap

import (
	"fmt"
	"time"
)

const StrategySubmarineSwap = "submarine_swap"

type Quote struct {
	LightningOutputSat int64     `json:"lightning_output_sat"`
	LockupAmountSat    int64     `json:"lockup_amount_sat"`
	ServiceFeeSat      int64     `json:"service_fee_sat"`
	MinerFeeSat        int64     `json:"miner_fee_sat"`
	FundingTxFeeSat    int64     `json:"funding_tx_fee_sat"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// Validate rejects an unsafe quote before the first irreversible action.
func (q Quote) Validate(now time.Time, maxAbsoluteSat, maxRelativePPM int64) error {
	if q.LightningOutputSat <= 0 || q.LockupAmountSat < q.LightningOutputSat {
		return fmt.Errorf("invalid submarine-swap quote amount")
	}
	if q.ExpiresAt.IsZero() || !q.ExpiresAt.After(now) {
		return fmt.Errorf("submarine-swap quote has expired")
	}
	if maxAbsoluteSat <= 0 || maxRelativePPM <= 0 {
		return fmt.Errorf("submarine swaps require both absolute and relative fee ceilings")
	}
	if q.ServiceFeeSat < 0 || q.MinerFeeSat < 0 || q.FundingTxFeeSat < 0 {
		return fmt.Errorf("invalid negative submarine-swap fee")
	}
	fee := q.ServiceFeeSat + q.MinerFeeSat + q.FundingTxFeeSat
	if fee > maxAbsoluteSat {
		return fmt.Errorf("submarine-swap fee %d sats exceeds absolute ceiling %d sats", fee, maxAbsoluteSat)
	}
	if fee*1_000_000 > q.LightningOutputSat*maxRelativePPM {
		return fmt.Errorf("submarine-swap fee exceeds relative ceiling %d ppm", maxRelativePPM)
	}
	return nil
}

// Record is safe to expose to APIs. Rescue material never belongs here.
type Record struct {
	ID                 string    `json:"id"`
	ReservationID      string    `json:"-"`
	TradeID            uint      `json:"trade_id,omitempty"`
	Strategy           string    `json:"strategy"`
	State              string    `json:"state"`
	LightningOutputSat int64     `json:"lightning_output_sat"`
	LockupAmountSat    int64     `json:"lockup_amount_sat"`
	LockupTxID         string    `json:"lockup_txid,omitempty"`
	InvoiceHash        string    `json:"invoice_hash,omitempty"`
	ServiceFeeSat      int64     `json:"service_fee_sat"`
	MinerFeeSat        int64     `json:"miner_fee_sat"`
	QuoteExpiresAt     time.Time `json:"quote_expires_at,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
