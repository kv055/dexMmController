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
)

// TransactionalLiquiditySettings is the singleton operator target profile.
type TransactionalLiquiditySettings struct {
	ID           int    `gorm:"primaryKey" json:"-"`
	FiatCurrency string `gorm:"column:fiat_currency" json:"fiat_currency"`
	// AmountSource is a virtual API field. It is derived from CustomAmount and
	// is intentionally not persisted in the database.
	AmountSource    string    `gorm:"-" json:"amount_source"`
	CustomAmount    *float64  `gorm:"column:custom_amount" json:"custom_amount"`
	TradeMode       string    `gorm:"column:trade_mode" json:"trade_mode"`
	TargetDirection string    `gorm:"column:target_direction" json:"target_direction"`
	ConfigVersion   int64     `gorm:"column:config_version" json:"config_version"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (TransactionalLiquiditySettings) TableName() string { return "transactional_liquidity_settings" }

var ErrTransactionalLiquiditySettingsNotFound = errors.New("transactional liquidity settings row not found")

type TransactionalLiquiditySettingsUpdate struct {
	FiatCurrency    string
	AmountSource    string
	CustomAmount    *float64
	TradeMode       string
	TargetDirection string
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
	if row.CustomAmount == nil {
		row.AmountSource = TransactionalLiquidityCappedVolume
	} else {
		row.AmountSource = TransactionalLiquidityCustomAmount
	}
	return row, nil
}

func (r *TransactionalLiquiditySettingsRepository) Update(ctx context.Context, u TransactionalLiquiditySettingsUpdate) (TransactionalLiquiditySettings, error) {
	fiat := strings.ToUpper(strings.TrimSpace(u.FiatCurrency))
	source := strings.TrimSpace(u.AmountSource)
	mode := strings.TrimSpace(u.TradeMode)
	direction := strings.ToLower(strings.TrimSpace(u.TargetDirection))
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
	if source == TransactionalLiquidityCustomAmount && (u.CustomAmount == nil || *u.CustomAmount <= 0) {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"custom_amount must be greater than zero for custom amount source"}
	}
	if u.CustomAmount != nil && *u.CustomAmount <= 0 {
		return TransactionalLiquiditySettings{}, TransactionalLiquidityValidationError{"custom_amount must be greater than zero"}
	}

	updates := map[string]any{
		"fiat_currency":    fiat,
		"custom_amount":    u.CustomAmount,
		"trade_mode":       mode,
		"target_direction": direction,
		"config_version":   gorm.Expr("config_version + 1"),
		"updated_at":       time.Now().UTC(),
	}
	res := r.db.WithContext(ctx).Model(&TransactionalLiquiditySettings{}).
		Where("id = ?", transactionalLiquidityRowID).Updates(updates)
	if res.Error != nil {
		return TransactionalLiquiditySettings{}, fmt.Errorf("update transactional liquidity settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		row := TransactionalLiquiditySettings{ID: transactionalLiquidityRowID, FiatCurrency: fiat, CustomAmount: u.CustomAmount, TradeMode: mode, TargetDirection: direction}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return TransactionalLiquiditySettings{}, fmt.Errorf("create transactional liquidity settings: %w", err)
		}
	}
	return r.Get(ctx)
}
