package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TelegramSettings is the single-row shared Telegram bot configuration consumed by
// dexTakerController (DEXMM webhook + trade-chat relay + anomaly alerts) and
// dexFiatController pushpush (PUSHPUSH webhook + fiat-arrival escalation). The
// schema is created by dexUnifiedApi/db/schema/22_telegram_settings.sql.
type TelegramSettings struct {
	ID                int    `gorm:"primaryKey" json:"-"`
	BotToken          string `gorm:"column:bot_token" json:"-"`
	ChatID            string `gorm:"column:chat_id" json:"chat_id"`
	DexmmWebhookURL   string `gorm:"column:dexmm_webhook_url" json:"dexmm_webhook_url"`
	PushpushWebhookURL string `gorm:"column:pushpush_webhook_url" json:"pushpush_webhook_url"`
	ConfigVersion     int64  `gorm:"column:config_version" json:"config_version"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName returns the database table name for the model.
func (TelegramSettings) TableName() string {
	return "telegram_settings"
}

const telegramSettingsRowID = 1

// ErrTelegramSettingsNotFound is returned when the single telegram_settings row is
// missing. Callers should treat this as "use env fallback / defaults".
var ErrTelegramSettingsNotFound = errors.New("telegram settings row not found")

// TelegramSettingsRepository reads and writes the shared telegram_settings row.
type TelegramSettingsRepository struct {
	db *gorm.DB
}

// NewTelegramSettingsRepository creates a new repository instance.
func NewTelegramSettingsRepository(db *gorm.DB) *TelegramSettingsRepository {
	return &TelegramSettingsRepository{db: db}
}

// Get returns the single telegram_settings row. Returns ErrTelegramSettingsNotFound
// when the row has not been seeded yet.
func (r *TelegramSettingsRepository) Get(ctx context.Context) (TelegramSettings, error) {
	var s TelegramSettings
	if err := r.db.WithContext(ctx).
		Where("id = ?", telegramSettingsRowID).
		First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return TelegramSettings{}, ErrTelegramSettingsNotFound
		}
		return TelegramSettings{}, fmt.Errorf("load telegram settings: %w", err)
	}
	return s, nil
}

// ConfigVersion returns the current config_version, or 0 when the row is absent.
// Cheap polling primitive used by the hot-reload pollers in both controllers.
func (r *TelegramSettingsRepository) ConfigVersion(ctx context.Context) (int64, error) {
	var v int64
	err := r.db.WithContext(ctx).
		Raw(`SELECT config_version FROM telegram_settings WHERE id = ?`, telegramSettingsRowID).
		Scan(&v).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("load telegram config_version: %w", err)
	}
	return v, nil
}

// TelegramSettingsUpdate carries the fields a caller may change. Empty string
// fields are ignored so partial updates don't clobber unrelated columns.
type TelegramSettingsUpdate struct {
	BotToken           *string
	ChatID             *string
	DexmmWebhookURL    *string
	PushpushWebhookURL *string
}

// Update applies a partial update to the single row, bumps config_version, and
// refreshes updated_at atomically. It returns the new full row.
func (r *TelegramSettingsRepository) Update(ctx context.Context, u TelegramSettingsUpdate) (TelegramSettings, error) {
	updates := map[string]any{
		"config_version": gorm.Expr("config_version + 1"),
		"updated_at":     time.Now().UTC(),
	}
	if u.BotToken != nil {
		updates["bot_token"] = strings.TrimSpace(*u.BotToken)
	}
	if u.ChatID != nil {
		updates["chat_id"] = strings.TrimSpace(*u.ChatID)
	}
	if u.DexmmWebhookURL != nil {
		updates["dexmm_webhook_url"] = strings.TrimSpace(*u.DexmmWebhookURL)
	}
	if u.PushpushWebhookURL != nil {
		updates["pushpush_webhook_url"] = strings.TrimSpace(*u.PushpushWebhookURL)
	}

	// Ensure the single row exists so the first PUT (before any seeding migration
	// ran) still succeeds; id defaults to 1 via the CHECK constraint.
	res := r.db.WithContext(ctx).
		Model(&TelegramSettings{}).
		Where("id = ?", telegramSettingsRowID).
		Updates(updates)
	if res.Error != nil {
		return TelegramSettings{}, fmt.Errorf("update telegram settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Row missing: insert with the requested fields then bump the version once
		// more so callers observing ConfigVersion see a stable, increasing value.
		row := TelegramSettings{ID: telegramSettingsRowID}
		if u.BotToken != nil {
			row.BotToken = strings.TrimSpace(*u.BotToken)
		}
		if u.ChatID != nil {
			row.ChatID = strings.TrimSpace(*u.ChatID)
		}
		if u.DexmmWebhookURL != nil {
			row.DexmmWebhookURL = strings.TrimSpace(*u.DexmmWebhookURL)
		}
		if u.PushpushWebhookURL != nil {
			row.PushpushWebhookURL = strings.TrimSpace(*u.PushpushWebhookURL)
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return TelegramSettings{}, fmt.Errorf("create telegram settings row: %w", err)
		}
	}
	return r.Get(ctx)
}
