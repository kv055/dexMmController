package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

var ErrLSPMagmaOrderNotFound = errors.New("LSP magma order not found")

// LSPMagmaOrder persists one Amboss Magma BLIP-0051 purchase attempt.
type LSPMagmaOrder struct {
	ID                  string    `gorm:"column:id;primaryKey" json:"id"`
	Pool                string    `gorm:"column:pool" json:"pool"`
	OrderID             string    `gorm:"column:order_id" json:"order_id"`
	PublicKey           string    `gorm:"column:public_key" json:"public_key"`
	LSPBalanceSat       int64     `gorm:"column:lsp_balance_sat" json:"lsp_balance_sat"`
	AnnounceChannel     bool      `gorm:"column:announce_channel" json:"announce_channel"`
	ChannelExpiryBlocks int       `gorm:"column:channel_expiry_blocks" json:"channel_expiry_blocks"`
	OrderState          string    `gorm:"column:order_state" json:"order_state"`
	PaymentState        string    `gorm:"column:payment_state" json:"payment_state"`
	Bolt11Invoice       string    `gorm:"column:bolt11_invoice" json:"bolt11_invoice"`
	OrderTotalSat       int64     `gorm:"column:order_total_sat" json:"order_total_sat"`
	FeeTotalSat         int64     `gorm:"column:fee_total_sat" json:"fee_total_sat"`
	FundingOutpoint     string    `gorm:"column:funding_outpoint" json:"funding_outpoint"`
	RemotePubkey        string    `gorm:"column:remote_pubkey" json:"remote_pubkey"`
	PaymentHash         string    `gorm:"column:payment_hash" json:"payment_hash"`
	AutoRegistered      bool      `gorm:"column:auto_registered" json:"auto_registered"`
	FailureReason       string    `gorm:"column:failure_reason" json:"failure_reason,omitempty"`
	CreatedAt           time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt           time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (LSPMagmaOrder) TableName() string { return "lsp_magma_orders" }

type LSPMagmaOrderRepository struct{ db *gorm.DB }

func NewLSPMagmaOrderRepository(db *gorm.DB) *LSPMagmaOrderRepository {
	return &LSPMagmaOrderRepository{db: db}
}

func (r *LSPMagmaOrderRepository) Create(ctx context.Context, row LSPMagmaOrder) error {
	now := time.Now().UTC()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("create LSP magma order: %w", err)
	}
	return nil
}

func (r *LSPMagmaOrderRepository) GetByID(ctx context.Context, id string) (LSPMagmaOrder, error) {
	var row LSPMagmaOrder
	if err := r.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return LSPMagmaOrder{}, ErrLSPMagmaOrderNotFound
		}
		return LSPMagmaOrder{}, fmt.Errorf("load LSP magma order: %w", err)
	}
	return row, nil
}

func (r *LSPMagmaOrderRepository) List(ctx context.Context, limit int) ([]LSPMagmaOrder, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []LSPMagmaOrder
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list LSP magma orders: %w", err)
	}
	return rows, nil
}

func (r *LSPMagmaOrderRepository) Incomplete(ctx context.Context) ([]LSPMagmaOrder, error) {
	var rows []LSPMagmaOrder
	if err := r.db.WithContext(ctx).
		Where("order_state NOT IN ?", []string{"COMPLETED", "FAILED"}).
		Order("updated_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list incomplete LSP magma orders: %w", err)
	}
	return rows, nil
}

func (r *LSPMagmaOrderRepository) Update(ctx context.Context, row LSPMagmaOrder) error {
	row.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&LSPMagmaOrder{}).Where("id = ?", row.ID).Updates(map[string]any{
		"order_state":       row.OrderState,
		"payment_state":     row.PaymentState,
		"bolt11_invoice":    row.Bolt11Invoice,
		"order_total_sat":   row.OrderTotalSat,
		"fee_total_sat":     row.FeeTotalSat,
		"funding_outpoint":  row.FundingOutpoint,
		"remote_pubkey":     row.RemotePubkey,
		"payment_hash":      row.PaymentHash,
		"auto_registered":   row.AutoRegistered,
		"failure_reason":    row.FailureReason,
		"updated_at":        row.UpdatedAt,
	})
	if res.Error != nil {
		return fmt.Errorf("update LSP magma order: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrLSPMagmaOrderNotFound
	}
	return nil
}

func (r *LSPMagmaOrderRepository) MarkPayment(ctx context.Context, id, paymentHash string) error {
	res := r.db.WithContext(ctx).Model(&LSPMagmaOrder{}).Where("id = ?", id).Updates(map[string]any{
		"payment_hash": strings.TrimSpace(paymentHash),
		"updated_at":   time.Now().UTC(),
	})
	if res.Error != nil {
		return fmt.Errorf("mark LSP magma payment: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrLSPMagmaOrderNotFound
	}
	return nil
}
