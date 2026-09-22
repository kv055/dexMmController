package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dexMmController/internal/lsp"
	"gorm.io/gorm"
)

var ErrLSPChannelConfigNotFound = errors.New("LSP channel configuration not found")

// LSPChannelConfig is the durable singleton for an operator-acquired channel.
// No lifecycle data is stored here: the controller never owns the channel.
type LSPChannelConfig struct {
	ID            string    `gorm:"primaryKey" json:"-"`
	ChannelPoint  string    `gorm:"column:channel_point" json:"channel_point"`
	RemotePubkey  string    `gorm:"column:remote_pubkey" json:"remote_pubkey"`
	ProviderLabel string    `gorm:"column:provider_label" json:"provider_label"`
	Enabled       bool      `gorm:"column:enabled" json:"enabled"`
	HeadroomSat   int64     `gorm:"column:headroom_sat" json:"headroom_sat"`
	MaxSwapFeeSat int64     `gorm:"column:max_swap_fee_sat" json:"max_swap_fee_sat"`
	MaxSwapFeePPM int64     `gorm:"column:max_swap_fee_ppm" json:"max_swap_fee_ppm"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (LSPChannelConfig) TableName() string { return "lsp_channel_config" }

type LSPChannelConfigRepository struct{ db *gorm.DB }

func NewLSPChannelConfigRepository(db *gorm.DB) *LSPChannelConfigRepository {
	return &LSPChannelConfigRepository{db: db}
}
func (r *LSPChannelConfigRepository) Get(ctx context.Context) (lsp.Config, error) {
	var row LSPChannelConfig
	if err := r.db.WithContext(ctx).Where("id = ?", "default").First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return lsp.Config{}, ErrLSPChannelConfigNotFound
		}
		return lsp.Config{}, fmt.Errorf("load LSP channel configuration: %w", err)
	}
	return lsp.Config{ChannelPoint: row.ChannelPoint, RemotePubkey: row.RemotePubkey, ProviderLabel: row.ProviderLabel, Enabled: row.Enabled, HeadroomSat: row.HeadroomSat, MaxSwapFeeSat: row.MaxSwapFeeSat, MaxSwapFeePPM: row.MaxSwapFeePPM, UpdatedAt: row.UpdatedAt}, nil
}
func (r *LSPChannelConfigRepository) Update(ctx context.Context, cfg lsp.Config) (lsp.Config, error) {
	cfg.ChannelPoint = strings.TrimSpace(cfg.ChannelPoint)
	cfg.RemotePubkey = strings.TrimSpace(cfg.RemotePubkey)
	cfg.ProviderLabel = strings.TrimSpace(cfg.ProviderLabel)
	if err := cfg.Validate(); err != nil {
		return lsp.Config{}, err
	}
	now := time.Now().UTC()
	row := LSPChannelConfig{ID: "default", ChannelPoint: cfg.ChannelPoint, RemotePubkey: cfg.RemotePubkey, ProviderLabel: cfg.ProviderLabel, Enabled: cfg.Enabled, HeadroomSat: cfg.HeadroomSat, MaxSwapFeeSat: cfg.MaxSwapFeeSat, MaxSwapFeePPM: cfg.MaxSwapFeePPM, UpdatedAt: now}
	if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
		return lsp.Config{}, fmt.Errorf("save LSP channel configuration: %w", err)
	}
	cfg.UpdatedAt = now
	return cfg, nil
}
