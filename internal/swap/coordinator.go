package swap

import (
	"context"
	"fmt"
	"time"
)

const (
	StatePrepared       = "prepared"
	StateFundingPending = "funding_pending"
	StateFunded         = "funded"
	StateSettled        = "settled"
	StateRefundPending  = "refund_pending"
	StateRefunded       = "refunded"
	StateNeedsReview    = "needs_review"
)

type Prepared struct {
	ID             string
	LockupAddress  string
	InvoiceHash    string
	RescueMaterial string
}
type RemoteStatus struct {
	State      string
	Settled    bool
	Failed     bool
	Refundable bool
}
type Provider interface {
	Quote(context.Context, int64) (Quote, error)
	Create(context.Context, Quote) (Prepared, error)
	Status(context.Context, string) (RemoteStatus, error)
	Refund(context.Context, string) error
}
type Wallet interface {
	SendOnChain(context.Context, string, int64, int64) (string, error)
}
type Sealer interface{ Seal(string) ([]byte, error) }
type Revalidator func(context.Context, Quote) error
type FeeLimitSource func(context.Context) (maxFeeSat, maxFeePPM int64, err error)

// Store persists before every irreversible transition. Implementations must
// enforce a single lockup transaction per swap ID.
type Store interface {
	Reserve(context.Context, string, uint, int64, int64, int64, int64, int64, int64, time.Time) error
	ReleaseReservation(context.Context, string) error
	FinalizeReservation(context.Context, string, time.Time) error
	Create(context.Context, Record, []byte) error
	MarkFundingPending(context.Context, string) error
	MarkFunded(context.Context, string, string) error
	SetState(context.Context, string, string) error
	Incomplete(context.Context) ([]Record, error)
}

type Coordinator struct {
	Provider                          Provider
	Wallet                            Wallet
	Store                             Store
	Sealer                            Sealer
	MaxFeeSat, MaxFeePPM, SatPerVByte int64
	FeeLimits                         FeeLimitSource
	Revalidate                        Revalidator
	PollInterval, timeOut             time.Duration
}

func (c *Coordinator) Execute(ctx context.Context, reservationID string, tradeID uint, outputSat, requiredOutboundSat, availableOutboundSat, inboundSat, onchainSat int64) (Record, error) {
	if c.Provider == nil || c.Wallet == nil || c.Store == nil || c.Sealer == nil {
		return Record{}, fmt.Errorf("submarine swap execution is not configured")
	}
	quote, err := c.Provider.Quote(ctx, outputSat)
	if err != nil {
		return Record{}, fmt.Errorf("get Boltz quote: %w", err)
	}
	maxFeeSat, maxFeePPM := c.MaxFeeSat, c.MaxFeePPM
	if c.FeeLimits != nil {
		maxFeeSat, maxFeePPM, err = c.FeeLimits(ctx)
		if err != nil {
			return Record{}, fmt.Errorf("load swap fee limits: %w", err)
		}
	}
	if err = quote.Validate(time.Now(), maxFeeSat, maxFeePPM); err != nil {
		return Record{}, err
	}
	if inboundSat < outputSat {
		return Record{}, fmt.Errorf("insufficient reserved inbound liquidity")
	}
	if onchainSat < quote.LockupAmountSat+quote.FundingTxFeeSat {
		return Record{}, fmt.Errorf("insufficient confirmed on-chain balance")
	}
	if requiredOutboundSat < outputSat || availableOutboundSat+outputSat < requiredOutboundSat {
		return Record{}, fmt.Errorf("swap output does not cover required outbound liquidity")
	}
	reservationExpires := quote.ExpiresAt
	if minimum := time.Now().Add(45 * time.Minute); reservationExpires.Before(minimum) {
		reservationExpires = minimum
	}
	if err = c.Store.Reserve(ctx, reservationID, tradeID, requiredOutboundSat, outputSat, quote.LockupAmountSat+quote.FundingTxFeeSat, availableOutboundSat+outputSat, inboundSat, onchainSat, reservationExpires); err != nil {
		return Record{}, err
	}
	keepReservation := false
	defer func() {
		if !keepReservation {
			_ = c.Store.ReleaseReservation(context.WithoutCancel(ctx), reservationID)
		}
	}()
	if c.Revalidate != nil {
		if err = c.Revalidate(ctx, quote); err != nil {
			return Record{}, fmt.Errorf("swap revalidation failed: %w", err)
		}
	}
	prepared, err := c.Provider.Create(ctx, quote)
	if err != nil {
		return Record{}, fmt.Errorf("create Boltz swap: %w", err)
	}
	if prepared.ID == "" || prepared.LockupAddress == "" {
		return Record{}, fmt.Errorf("Boltz returned incomplete swap")
	}
	sealed, err := c.Sealer.Seal(prepared.RescueMaterial)
	if err != nil {
		return Record{}, fmt.Errorf("encrypt Boltz rescue material: %w", err)
	}
	now := time.Now().UTC()
	rec := Record{ID: prepared.ID, ReservationID: reservationID, TradeID: tradeID, Strategy: StrategySubmarineSwap, State: StatePrepared, LightningOutputSat: quote.LightningOutputSat, LockupAmountSat: quote.LockupAmountSat, InvoiceHash: prepared.InvoiceHash, ServiceFeeSat: quote.ServiceFeeSat, MinerFeeSat: quote.MinerFeeSat + quote.FundingTxFeeSat, QuoteExpiresAt: quote.ExpiresAt, CreatedAt: now, UpdatedAt: now}
	if err = c.Store.Create(ctx, rec, sealed); err != nil {
		return Record{}, err
	}
	if c.Revalidate != nil {
		if err = c.Revalidate(ctx, quote); err != nil {
			return rec, fmt.Errorf("swap funding revalidation failed: %w", err)
		}
	}
	if err = c.Store.MarkFundingPending(ctx, prepared.ID); err != nil {
		return rec, err
	}
	rate := c.SatPerVByte
	if rate <= 0 {
		rate = 2
	}
	txid, err := c.Wallet.SendOnChain(ctx, prepared.LockupAddress, quote.LockupAmountSat, rate)
	if err != nil {
		_ = c.Store.SetState(context.WithoutCancel(ctx), prepared.ID, StateNeedsReview)
		keepReservation = true
		return rec, fmt.Errorf("fund Boltz lockup: %w", err)
	}
	if err = c.Store.MarkFunded(ctx, prepared.ID, txid); err != nil {
		keepReservation = true
		return rec, err
	}
	rec.State = StateFunded
	rec.LockupTxID = txid
	keepReservation = true
	if err = c.wait(ctx, &rec); err != nil {
		return rec, err
	}
	if rec.State == StateRefunded {
		keepReservation = false
		return rec, nil
	}
	if err = c.Store.FinalizeReservation(ctx, reservationID, time.Now().Add(15*time.Minute)); err != nil {
		return rec, fmt.Errorf("finalize outbound reservation: %w", err)
	}
	keepReservation = true
	return rec, nil
}

func (c *Coordinator) Release(ctx context.Context, reservationID string) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.ReleaseReservation(ctx, reservationID)
}

// ReserveOutbound protects an already-sufficient Lightning balance from being
// reused by another trade while the caller takes and pays the DEX offer.
func (c *Coordinator) ReserveOutbound(ctx context.Context, reservationID string, tradeID uint, requiredSat, availableSat int64, expiresAt time.Time) error {
	if c.Store == nil {
		return fmt.Errorf("liquidity reservation store is not configured")
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(15 * time.Minute)
	}
	return c.Store.Reserve(ctx, reservationID, tradeID, requiredSat, 0, 0, availableSat, 0, 0, expiresAt)
}

func (c *Coordinator) wait(ctx context.Context, rec *Record) error {
	interval := c.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	timeout := c.timeOut
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		status, err := c.Provider.Status(ctx, rec.ID)
		if err == nil {
			if status.Settled {
				rec.State = StateSettled
				return c.Store.SetState(ctx, rec.ID, StateSettled)
			}
			if status.Failed {
				if setErr := c.Store.SetState(ctx, rec.ID, StateRefundPending); setErr != nil {
					return setErr
				}
				if status.Refundable {
					if refundErr := c.Provider.Refund(ctx, rec.ID); refundErr == nil {
						rec.State = StateRefunded
						return c.Store.SetState(ctx, rec.ID, StateRefunded)
					}
				}
				return fmt.Errorf("Boltz swap %s failed; refund pending", rec.ID)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Boltz swap %s settlement timed out", rec.ID)
		case <-ticker.C:
		}
	}
}

// Recover resumes only swaps which were already persisted; it never quotes or creates.
func (c *Coordinator) Recover(ctx context.Context) error {
	rows, err := c.Store.Incomplete(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].State == StateFunded || rows[i].State == StateRefundPending {
			if waitErr := c.wait(ctx, &rows[i]); waitErr != nil {
				return waitErr
			}
			if rows[i].ReservationID != "" {
				if rows[i].State == StateSettled {
					if finalizeErr := c.Store.FinalizeReservation(ctx, rows[i].ReservationID, time.Now().Add(15*time.Minute)); finalizeErr != nil {
						return finalizeErr
					}
				} else if rows[i].State == StateRefunded {
					if releaseErr := c.Store.ReleaseReservation(ctx, rows[i].ReservationID); releaseErr != nil {
						return releaseErr
					}
				}
			}
		}
	}
	return nil
}
