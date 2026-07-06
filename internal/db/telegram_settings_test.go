package db

import (
	"context"
	"os"
	"testing"
)

// TestTelegramSettingsUpdatePersistsAndBumpsVersion exercises the repository
// against a real Postgres. Skipped unless DEXMM_TEST_DB is set to a DSN whose
// role can create the telegram_settings table.
func TestTelegramSettingsUpdatePersistsAndBumpsVersion(t *testing.T) {
	dsn := os.Getenv("DEXMM_TEST_DB")
	if dsn == "" {
		t.Skip("set DEXMM_TEST_DB to run the Postgres integration test")
	}
	gormDB, err := InitDB(dsn)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer Close(gormDB)
	ctx := context.Background()

	if err := gormDB.Exec(`
CREATE TABLE IF NOT EXISTS telegram_settings (
    id INT PRIMARY KEY DEFAULT 1,
    bot_token TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    dexmm_webhook_url TEXT NOT NULL DEFAULT '',
    pushpush_webhook_url TEXT NOT NULL DEFAULT '',
    config_version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT telegram_settings_single_row CHECK (id = 1)
)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	gormDB.Exec(`DELETE FROM telegram_settings`)

	repo := NewTelegramSettingsRepository(gormDB)

	// Row starts absent.
	if _, err := repo.Get(ctx); err != ErrTelegramSettingsNotFound {
		t.Fatalf("expected ErrTelegramSettingsNotFound, got %v", err)
	}

	chatID := "123"
	dexmm := "https://h/wh"
	updated, err := repo.Update(ctx, TelegramSettingsUpdate{
		BotToken:        strPtr("tok"),
		ChatID:          &chatID,
		DexmmWebhookURL: &dexmm,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.ConfigVersion != 1 {
		t.Errorf("first version = %d, want 1", updated.ConfigVersion)
	}
	if updated.BotToken != "tok" || updated.ChatID != "123" {
		t.Errorf("row = %+v", updated)
	}

	// Second update bumps the version.
	pushpush := "https://h/pp"
	updated2, err := repo.Update(ctx, TelegramSettingsUpdate{PushpushWebhookURL: &pushpush})
	if err != nil {
		t.Fatalf("Update 2: %v", err)
	}
	if updated2.ConfigVersion != 2 {
		t.Errorf("second version = %d, want 2", updated2.ConfigVersion)
	}
	if updated2.PushpushWebhookURL != "https://h/pp" {
		t.Errorf("pushpush url = %q", updated2.PushpushWebhookURL)
	}
	// Untouched fields preserved.
	if updated2.BotToken != "tok" || updated2.ChatID != "123" || updated2.DexmmWebhookURL != "https://h/wh" {
		t.Errorf("untouched fields changed: %+v", updated2)
	}

	// ConfigVersion read primitive agrees.
	if v, err := repo.ConfigVersion(ctx); err != nil || v != 2 {
		t.Fatalf("ConfigVersion = %v err=%v, want 2", v, err)
	}
}

func strPtr(s string) *string { return &s }
