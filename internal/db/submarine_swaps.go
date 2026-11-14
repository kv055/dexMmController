package db

import (
	"commons/credentialcrypto"
	"context"
	"errors"
	"fmt"
	"time"

	"dexTakerController/internal/swap"
	"gorm.io/gorm"
)

// SubmarineSwap keeps recovery material encrypted and out of API responses.
type SubmarineSwap struct {
	ID                      string    `gorm:"primaryKey;column:id"`
	ReservationID           string    `gorm:"column:reservation_id"`
	TradeID                 uint      `gorm:"column:trade_id"`
	State                   string    `gorm:"column:state"`
	LightningOutputSat      int64     `gorm:"column:lightning_output_sat"`
	LockupAmountSat         int64     `gorm:"column:lockup_amount_sat"`
	LockupTxID              string    `gorm:"column:lockup_txid"`
	InvoiceHash             string    `gorm:"column:invoice_hash"`
	ServiceFeeSat           int64     `gorm:"column:service_fee_sat"`
	MinerFeeSat             int64     `gorm:"column:miner_fee_sat"`
	QuoteExpiresAt          time.Time `gorm:"column:quote_expires_at"`
	RescueMaterialEncrypted []byte    `gorm:"column:rescue_material_encrypted" json:"-"`
	CreatedAt               time.Time `gorm:"column:created_at"`
	UpdatedAt               time.Time `gorm:"column:updated_at"`
}

type LiquidityReservation struct {
	ID          string    `gorm:"primaryKey;column:id"`
	TradeID     uint      `gorm:"column:trade_id"`
	OutboundSat int64     `gorm:"column:outbound_sat"`
	InboundSat  int64     `gorm:"column:inbound_sat"`
	OnchainSat  int64     `gorm:"column:onchain_sat"`
	State       string    `gorm:"column:state"`
	ExpiresAt   time.Time `gorm:"column:expires_at"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (LiquidityReservation) TableName() string { return "liquidity_reservations" }

func (SubmarineSwap) TableName() string { return "submarine_swaps" }

type SubmarineSwapRepository struct{ db *gorm.DB }

func NewSubmarineSwapRepository(db *gorm.DB) *SubmarineSwapRepository {
	return &SubmarineSwapRepository{db: db}
}
func (r *SubmarineSwapRepository) List(ctx context.Context, limit int) ([]swap.Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []SubmarineSwap
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list submarine swaps: %w", err)
	}
	out := make([]swap.Record, 0, len(rows))
	for _, v := range rows {
		out = append(out, swap.Record{ID: v.ID, ReservationID: v.ReservationID, TradeID: v.TradeID, Strategy: swap.StrategySubmarineSwap, State: v.State, LightningOutputSat: v.LightningOutputSat, LockupAmountSat: v.LockupAmountSat, LockupTxID: v.LockupTxID, InvoiceHash: v.InvoiceHash, ServiceFeeSat: v.ServiceFeeSat, MinerFeeSat: v.MinerFeeSat, QuoteExpiresAt: v.QuoteExpiresAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	return out, nil
}

func (r *SubmarineSwapRepository) Reserve(ctx context.Context, id string, tradeID uint, requiredOutbound, requiredInbound, requiredOnchain, availableOutbound, availableInbound, availableOnchain int64, expires time.Time) error {
	if id == "" || expires.Before(time.Now()) {
		return fmt.Errorf("invalid liquidity reservation")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialise reserve decisions across controller instances.
		if err := tx.Exec("LOCK TABLE liquidity_reservations IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return err
		}
		var existing LiquidityReservation
		findErr := tx.Where("id = ?", id).First(&existing).Error
		if findErr == nil && existing.State == "active" && existing.ExpiresAt.After(time.Now()) {
			if existing.TradeID == tradeID && existing.OutboundSat == requiredOutbound && existing.InboundSat == requiredInbound && existing.OnchainSat == requiredOnchain {
				return nil
			}
			return fmt.Errorf("liquidity reservation %s already exists with different requirements", id)
		}
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		if findErr == nil {
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
		}
		var reserved struct{ Outbound, Inbound, Onchain int64 }
		if err := tx.Raw("SELECT COALESCE(SUM(outbound_sat),0) AS outbound, COALESCE(SUM(inbound_sat),0) AS inbound, COALESCE(SUM(onchain_sat),0) AS onchain FROM liquidity_reservations WHERE state = 'active' AND expires_at > NOW()").Scan(&reserved).Error; err != nil {
			return err
		}
		if reserved.Outbound+requiredOutbound > availableOutbound {
			return fmt.Errorf("insufficient unreserved outbound liquidity")
		}
		if reserved.Inbound+requiredInbound > availableInbound {
			return fmt.Errorf("insufficient unreserved inbound liquidity")
		}
		if reserved.Onchain+requiredOnchain > availableOnchain {
			return fmt.Errorf("insufficient unreserved confirmed on-chain balance")
		}
		row := LiquidityReservation{ID: id, TradeID: tradeID, OutboundSat: requiredOutbound, InboundSat: requiredInbound, OnchainSat: requiredOnchain, State: "active", ExpiresAt: expires, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("reserve liquidity: %w", err)
		}
		return nil
	})
}
func (r *SubmarineSwapRepository) ReleaseReservation(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&LiquidityReservation{}).Where("id = ? AND state = ?", id, "active").Update("state", "released").Error
}
func (r *SubmarineSwapRepository) FinalizeReservation(ctx context.Context, id string, expiresAt time.Time) error {
	return r.db.WithContext(ctx).Model(&LiquidityReservation{}).Where("id = ? AND state = ?", id, "active").Updates(map[string]any{"inbound_sat": 0, "onchain_sat": 0, "expires_at": expiresAt}).Error
}
func (r *SubmarineSwapRepository) Create(ctx context.Context, rec swap.Record, rescue []byte) error {
	row := SubmarineSwap{ID: rec.ID, ReservationID: rec.ReservationID, TradeID: rec.TradeID, State: rec.State, LightningOutputSat: rec.LightningOutputSat, LockupAmountSat: rec.LockupAmountSat, InvoiceHash: rec.InvoiceHash, ServiceFeeSat: rec.ServiceFeeSat, MinerFeeSat: rec.MinerFeeSat, QuoteExpiresAt: rec.QuoteExpiresAt, RescueMaterialEncrypted: rescue, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt}
	return r.db.WithContext(ctx).Create(&row).Error
}
func (r *SubmarineSwapRepository) MarkFundingPending(ctx context.Context, id string) error {
	return r.setState(ctx, id, swap.StateFundingPending)
}
func (r *SubmarineSwapRepository) MarkFunded(ctx context.Context, id, txid string) error {
	res := r.db.WithContext(ctx).Model(&SubmarineSwap{}).Where("id = ? AND state = ? AND lockup_txid = ''", id, swap.StateFundingPending).Updates(map[string]any{"state": swap.StateFunded, "lockup_txid": txid, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("swap %s lockup transaction already recorded or state changed", id)
	}
	return nil
}
func (r *SubmarineSwapRepository) SetState(ctx context.Context, id, state string) error {
	return r.setState(ctx, id, state)
}
func (r *SubmarineSwapRepository) setState(ctx context.Context, id, state string) error {
	return r.db.WithContext(ctx).Model(&SubmarineSwap{}).Where("id = ?", id).Updates(map[string]any{"state": state, "updated_at": time.Now().UTC()}).Error
}
func (r *SubmarineSwapRepository) Incomplete(ctx context.Context) ([]swap.Record, error) {
	var rows []SubmarineSwap
	if err := r.db.WithContext(ctx).Where("state IN ?", []string{swap.StateFunded, swap.StateRefundPending}).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]swap.Record, 0, len(rows))
	for _, v := range rows {
		out = append(out, swap.Record{ID: v.ID, ReservationID: v.ReservationID, TradeID: v.TradeID, State: v.State, LockupTxID: v.LockupTxID, LightningOutputSat: v.LightningOutputSat, LockupAmountSat: v.LockupAmountSat})
	}
	return out, nil
}

func (r *SubmarineSwapRepository) LoadRecovery(ctx context.Context, id string) (string, error) {
	var row SubmarineSwap
	if err := r.db.WithContext(ctx).Select("rescue_material_encrypted").Where("id = ?", id).First(&row).Error; err != nil {
		return "", err
	}
	plain, err := credentialcrypto.Decrypt(string(row.RescueMaterialEncrypted))
	if err != nil {
		return "", fmt.Errorf("decrypt Boltz rescue material: %w", err)
	}
	return plain, nil
}
