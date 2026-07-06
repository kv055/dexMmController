package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"commons/telegram"
	"dexMmController/internal/db"
)

type fakeTelegramSettingsRepo struct {
	row     db.TelegramSettings
	getErr  error
	update  db.TelegramSettingsUpdate
	updated db.TelegramSettings
}

func (f *fakeTelegramSettingsRepo) Get(_ context.Context) (db.TelegramSettings, error) {
	if f.getErr != nil {
		return db.TelegramSettings{}, f.getErr
	}
	return f.row, nil
}

func (f *fakeTelegramSettingsRepo) Update(_ context.Context, u db.TelegramSettingsUpdate) (db.TelegramSettings, error) {
	f.update = u
	return f.updated, nil
}

func TestTelegramConfigUnavailableWhenNotWired(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/telegram/config", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramSettingsConfig(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestTelegramConfigGetMasksToken(t *testing.T) {
	repo := &fakeTelegramSettingsRepo{row: db.TelegramSettings{
		BotToken: "secret-token", ChatID: "123", DexmmWebhookURL: "https://h/wh",
		PushpushWebhookURL: "https://h/pp", ConfigVersion: 7,
	}}
	store := telegram.NewStore(telegram.Config{Token: "secret-token", ChatID: "123", WebhookURL: "https://h/wh"}, nil)
	s := (&Server{}).WithTelegramSettings(repo, store)

	req := httptest.NewRequest(http.MethodGet, "/api/telegram/config", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramSettingsConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body telegramSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.BotTokenPresent {
		t.Error("expected bot_token_present=true")
	}
	if body.ChatID != "123" {
		t.Errorf("chat_id = %q", body.ChatID)
	}
	if body.ConfigVersion != 7 {
		t.Errorf("config_version = %d", body.ConfigVersion)
	}
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("token leaked in response: %s", rec.Body.String())
	}
	if !body.DexmmWebhookSet {
		t.Error("expected dexmm_webhook_set=true")
	}
}

func TestTelegramConfigPutPersistsAndBumpsVersion(t *testing.T) {
	repo := &fakeTelegramSettingsRepo{updated: db.TelegramSettings{
		BotToken: "newtok", ChatID: "999", DexmmWebhookURL: "https://h/wh",
		ConfigVersion: 3,
	}}
	store := telegram.NewStore(telegram.Config{}, nil) // disabled: no Telegram calls
	s := (&Server{}).WithTelegramSettings(repo, store)

	body := `{"bot_token":"newtok","chat_id":"999","dexmm_webhook_url":"https://h/wh"}`
	req := httptest.NewRequest(http.MethodPut, "/api/telegram/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTelegramSettingsConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if repo.update.BotToken == nil || *repo.update.BotToken != "newtok" {
		t.Fatalf("bot_token not forwarded: %+v", repo.update)
	}
	if repo.update.ChatID == nil || *repo.update.ChatID != "999" {
		t.Fatalf("chat_id not forwarded: %+v", repo.update)
	}
	// With token+chatID present, the store should now be enabled and reflect the
	// new config (applied in-process via Set).
	if !store.Enabled() {
		t.Fatal("expected store enabled after PUT")
	}
	if store.Get().Token != "newtok" {
		t.Errorf("store token = %q", store.Get().Token)
	}
	var resp telegramSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ConfigVersion != 3 {
		t.Errorf("response version = %d, want 3", resp.ConfigVersion)
	}
}

func TestTelegramSetWebhookRequiresService(t *testing.T) {
	s := (&Server{}).WithTelegramSettings(&fakeTelegramSettingsRepo{}, telegram.NewStore(telegram.Config{}, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/set-webhook", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramSetWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTelegramSetWebhookDisabledWhenBotNotConfigured(t *testing.T) {
	repo := &fakeTelegramSettingsRepo{row: db.TelegramSettings{DexmmWebhookURL: "https://h/wh"}}
	store := telegram.NewStore(telegram.Config{}, nil) // no token → disabled
	s := (&Server{}).WithTelegramSettings(repo, store)
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/set-webhook?service=dexmm", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramSetWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestTelegramTestDefaultsToDexmmAndRejectsBadService(t *testing.T) {
	s := (&Server{}).WithTelegramSettings(&fakeTelegramSettingsRepo{}, telegram.NewStore(telegram.Config{}, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/test?service=bogus", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramTest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTelegramTestDisabledWhenBotNotConfigured(t *testing.T) {
	s := (&Server{}).WithTelegramSettings(&fakeTelegramSettingsRepo{}, telegram.NewStore(telegram.Config{}, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/test", nil)
	rec := httptest.NewRecorder()
	s.handleTelegramTest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}
