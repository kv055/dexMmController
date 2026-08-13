package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const transactionalLiquidityRowID = 1

const (
	TransactionalLiquidityCustomAmount = "custom"
	TransactionalLiquidityCappedVolume = "capped_volume"
	TransactionalLiquidityTargetBid    = "bid"
	TransactionalLiquidityTargetAsk    = "ask"
	TransactionalLiquidityOwnChannels  = "own_channels"
	TransactionalLiquidityLSPChannel   = "lsp_channel"
	TransactionalLiquidityFeeEconomic  = "economic"
)

// TransactionalLiquiditySettings is the singleton operator target profile.
type TransactionalLiquiditySettings struct {
	ID                       int       `gorm:"primaryKey" json:"-"`
	FiatCurrency             string    `gorm:"column:fiat_currency" json:"fiat_currency"`
	AmountSource             string    `gorm:"column:amount_source" json:"amount_source"`
	AmountFiat               *float64  `gorm:"column:amount_fiat" json:"amount_fiat"`
	TradeMode                string    `gorm:"column:trade_mode" json:"trade_mode"`
	TargetDirection          string    `gorm:"column:target_direction" json:"target_direction"`
	LightningLiquiditySource string    `gorm:"column:lightning_liquidity_source" json:"lightning_liquidity_source"`
	FeeTier                  string    `gorm:"column:fee_tier" json:"fee_tier"`
	CoupledHedge             bool      `gorm:"column:coupled_hedge" json:"coupled_hedge"`
	ConfigVersion            int64     `gorm:"column:config_version" json:"config_version"`
	UpdatedAt                time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (TransactionalLiquiditySettings) TableName() string { return "transactional_liquidity_settings" }

var ErrTransactionalLiquiditySettingsNotFound = errors.New("transactional liquidity settings row not found")

type TransactionalLiquiditySettingsUpdate struct {
	FiatCurrency             string
	AmountSource             string
	AmountFiat               *float64
	TradeMode                string
	TargetDirection          string
	LightningLiquiditySource string
	FeeTier                  string
	CoupledHedge             bool
}

type TransactionalLiquidityValidationError struct{ Message string }

func (e TransactionalLiquidityValidationError) Error() string { return e.Message }

type TransactionalLiquiditySettingsRepository struct{ db *gorm.DB }

func NewTransactionalLiquiditySettingsRepository(db *gorm.DB) *TransactionalLiquiditySettingsRepository {
	return &TransactionalLiquiditySettingsRepository{db: db}
}

func (r *TransactionalLiquiditySettingsRepository) Get(ctx context.Context) (TransactionalLiquiditySettings, error) {
	var row TransactionalLiquiditySettings
	if err := r.db.WithContext(ctx).Where("id = ?", transactionalLiquidityRowID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return TransactionalLiquiditySettings{}, ErrTransactionalLiquiditySettingsNotFound
		}
		return TransactionalLiquiditySettings{}, fmt.Errorf("load transactional liquidity settings: %w", err)
	}
	return row, nil
}

func (r *TransactionalLiquiditySettingsRepository) Update(ctx context.Context, u TransactionalLiquiditySettingsUpdate) (TransactionalLiquiditySettings, error) {
	fiat := strings.ToUpper(strings.TrimSpace(u.FiatCurrency))
	source := strings.TrimSpace(u.AmountSource)
	mode := strings.TrimSpace(u.TradeMode)
	direction := strings.ToLower(strings.TrimSpace(u.TargetDirection))
	lightningSource := strings.ToLower(strings.TrimSpace(u.LightningLiquiditySource))
	feeTier := strings.ToLower(strings.TrimSpace(u.FeeTier))
	if fiat == "" {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"fiat_currency is required"}
	}
	validFiat := map[string]bool{"EUR": true, "USD": true, "GBP": true, "AUD": true, "CHF": true, "SEK": true, "NOK": true, "CAD": true}
	if !validFiat[fiat] {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"fiat_currency is unsupported"}
	}
	if source != TransactionalLiquidityCustomAmount && source != TransactionalLiquidityCappedVolume {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"amount_source must be custom or capped_volume"}
	}
	if mode != "hedgefull_single_collateral" && mode != "hedgeless_double_collateral" {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"trade_mode is unsupported"}
	}
	if direction != TransactionalLiquidityTargetBid && direction != TransactionalLiquidityTargetAsk {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"target_direction must be bid or ask"}
	}
	if lightningSource != TransactionalLiquidityOwnChannels && lightningSource != TransactionalLiquidityLSPChannel {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"lightning_liquidity_source must be own_channels or lsp_channel"}
	}
	if u.AmountFiat == nil || *u.AmountFiat <= 0 {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"amount_fiat must be greater than zero"}
	}
	if feeTier == "" {
		feeTier = TransactionalLiquidityFeeEconomic
	}
	if feeTier != TransactionalLiquidityFeeEconomic {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"fee_tier must be economic"}
	}
	current, currentErr := r.Get(ctx)
	if currentErr == nil && current.FiatCurrency == fiat && current.AmountSource == source &&
		floatPointersEqual(current.AmountFiat, u.AmountFiat) && current.TradeMode == mode &&
		current.TargetDirection == direction && current.LightningLiquiditySource == lightningSource &&
		current.FeeTier == feeTier && current.CoupledHedge == u.CoupledHedge {
		return current, nil
	}
	if currentErr != nil && !errors.Is(currentErr, ErrTransactionalLiquiditySettingsNotFound) {
		return TransactionalLiquiditySettings{}, currentErr
	}

	updates := map[string]any{
		"fiat_currency":              fiat,
		"amount_source":              source,
		"amount_fiat":                u.AmountFiat,
		"trade_mode":                 mode,
		"target_direction":           direction,
		"lightning_liquidity_source": lightningSource,
		"fee_tier":                   feeTier,
		"coupled_hedge":              u.CoupledHedge,
		"config_version":             gorm.Expr("config_version + 1"),
		"updated_at":                 time.Now().UTC(),
	}
	res := r.db.WithContext(ctx).Model(&TransactionalLiquiditySettings{}).
		Where("id = ?", transactionalLiquidityRowID).Updates(updates)
	if res.Error != nil {
		return TransactionalLiquiditySettings{}, fmt.Errorf("update transactional liquidity settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		row := TransactionalLiquiditySettings{ID: transactionalLiquidityRowID, FiatCurrency: fiat, AmountSource: source, AmountFiat: u.AmountFiat, TradeMode: mode, TargetDirection: direction, LightningLiquiditySource: lightningSource, FeeTier: feeTier, CoupledHedge: u.CoupledHedge, ConfigVersion: 1}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return TransactionalLiquiditySettings{}, fmt.Errorf("create transactional liquidity settings: %w", err)
		}
	}
	return r.Get(ctx)
}

func floatPointersEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
