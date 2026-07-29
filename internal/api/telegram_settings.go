package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"commons/telegram"
	"dexMmController/internal/db"
)

// telegramClientSetter is satisfied by anything that can swap its outbound
// Telegram client at runtime (both *telegram.ChatRelay and *dexnotify.AnomalyNotifier
// do). Lets the settings handler push a hot-reloaded client to every consumer
// without the api package importing dexnotify.
type telegramClientSetter interface {
	SetClient(*telegram.Client)
}

// telegramSettingsResponse is the public shape of GET/PUT /api/telegram/config.
// The bot token is never serialized; only a presence flag is exposed.
type telegramSettingsResponse struct {
	BotTokenPresent    bool   `json:"bot_token_present"`
	ChatID             string `json:"chat_id"`
	DexmmWebhookURL    string `json:"dexmm_webhook_url"`
	PushpushWebhookURL string `json:"pushpush_webhook_url"`
	DexmmWebhookSet    bool   `json:"dexmm_webhook_set"`
	PushpushWebhookSet bool   `json:"pushpush_webhook_set"`
	ConfigVersion      int64  `json:"config_version"`
}

// telegramSettingsUpdateRequest is the body of PUT /api/telegram/config. Every
// field is optional; an omitted field leaves the stored value untouched so the
// UI can save partial edits (e.g. just the chat ID) without clobbering the
// token. bot_token is only written when a non-empty string is provided.
type telegramSettingsUpdateRequest struct {
	BotToken           *string `json:"bot_token,omitempty"`
	ChatID             *string `json:"chat_id,omitempty"`
	PushpushWebhookURL *string `json:"pushpush_webhook_url,omitempty"`
}

// telegramSettingsBackend is the subset of the Telegram settings repository the
// API handlers need. *db.TelegramSettingsRepository satisfies it; tests inject
// a fake so the handler can be exercised without Postgres.
type telegramSettingsBackend interface {
	Get(context.Context) (db.TelegramSettings, error)
	Update(context.Context, db.TelegramSettingsUpdate) (db.TelegramSettings, error)
}

// WithTelegramSettings attaches the shared Telegram settings repository and the
// runtime config store to the API server, enabling the /api/telegram/* routes.
// extraSetters receives the rebuilt client on every config change alongside the
// trade-chat relay (already wired via WithTelegramRelay); pass the anomaly
// notifier here so its alerts pick up the new token without a restart.
func (s *Server) WithTelegramSettings(repo telegramSettingsBackend, store *telegram.Store, extraSetters ...telegramClientSetter) *Server {
	s.telegramSettingsRepo = repo
	s.telegramStore = store
	s.telegramExtraSetters = extraSetters
	return s
}

// handleTelegramSettingsConfig serves GET (read) and PUT (update) for the shared
// Telegram bot configuration. PUT persists, bumps config_version, applies the
// change in-process via the store, and re-registers the DEXMM webhook with
// Telegram when both token+chat ID and a DEXMM webhook URL are present.
func (s *Server) handleTelegramSettingsConfig(w http.ResponseWriter, r *http.Request) {
	if s.telegramSettingsRepo == nil || s.telegramStore == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("telegram settings not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveTelegramSettings(w, r)
	case http.MethodPut:
		s.updateTelegramSettings(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
	}
}

func (s *Server) serveTelegramSettings(w http.ResponseWriter, r *http.Request) {
	row, err := s.telegramSettingsRepo.Get(r.Context())
	if err != nil && err != db.ErrTelegramSettingsNotFound {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rowToResponse(row, s.telegramStore))
}

func (s *Server) updateTelegramSettings(w http.ResponseWriter, r *http.Request) {
	var req telegramSettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	managedWebhookURL := strings.TrimSpace(s.telegramStore.Get().WebhookURL)
	if managedWebhookURL == "" {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("DEXMM webhook URL is not configured by this deployment"))
		return
	}
	updated, err := s.telegramSettingsRepo.Update(r.Context(), db.TelegramSettingsUpdate{
		BotToken:           req.BotToken,
		ChatID:             req.ChatID,
		DexmmWebhookURL:    &managedWebhookURL,
		PushpushWebhookURL: req.PushpushWebhookURL,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply in-process so dexMmController's own client/relay pick up the change
	// immediately (the poller would also catch it within ~10s).
	s.applyTelegramConfig(updated)

	// Re-register the DEXMM webhook with Telegram when fully configured.
	if s.telegramStore.Enabled() && strings.TrimSpace(updated.DexmmWebhookURL) != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		if err := s.telegramStore.Client().SetWebhook(ctx, updated.DexmmWebhookURL); err != nil {
			cancel()
			// Non-fatal: row is persisted. Surface via a warning header so the UI
			// can still read the saved config from the body.
			w.Header().Set("X-Telegram-Warning", "dexmm setWebhook: "+err.Error())
			writeJSON(w, http.StatusOK, rowToResponse(updated, s.telegramStore))
			return
		}
		cancel()
	}

	writeJSON(w, http.StatusOK, rowToResponse(updated, s.telegramStore))
}

func (s *Server) applyTelegramConfig(row db.TelegramSettings) {
	s.telegramStore.Set(telegram.Config{Token: row.BotToken, ChatID: row.ChatID, WebhookURL: firstNonEmpty(row.DexmmWebhookURL, s.telegramStore.Get().WebhookURL)})
	client := s.telegramStore.Client()
	if s.telegramRelay != nil {
		s.telegramRelay.SetClient(client)
	}
	for _, setter := range s.telegramExtraSetters {
		if setter != nil {
			setter.SetClient(client)
		}
	}
}

// handleTelegramSetWebhook re-registers one service's webhook URL with Telegram.
// service=dexmm uses the dexmm_webhook_url column; service=pushpush uses
// pushpush_webhook_url. Both share the single bot token.
func (s *Server) handleTelegramSetWebhook(w http.ResponseWriter, r *http.Request) {
	if s.telegramSettingsRepo == nil || s.telegramStore == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("telegram settings not configured"))
		return
	}
	service := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("service")))
	if service != "dexmm" && service != "pushpush" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("service query parameter must be 'dexmm' or 'pushpush'"))
		return
	}
	row, err := s.telegramSettingsRepo.Get(r.Context())
	if err != nil && err != db.ErrTelegramSettingsNotFound {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	url := firstNonEmpty(row.DexmmWebhookURL, s.telegramStore.Get().WebhookURL)
	if service == "pushpush" {
		url = row.PushpushWebhookURL
	}
	if strings.TrimSpace(url) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no webhook URL configured for service %q", service))
		return
	}
	if !s.telegramStore.Enabled() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("telegram bot token/chat_id not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.telegramStore.Client().SetWebhook(ctx, url); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": url})
}

// handleTelegramTest sends a one-way test message to the configured chat to
// verify the bot token + chat ID. The service parameter is accepted for
// symmetry with set-webhook but the message is identical (shared chat).
func (s *Server) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	if s.telegramSettingsRepo == nil || s.telegramStore == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("telegram settings not configured"))
		return
	}
	service := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("service")))
	if service == "" {
		service = "dexmm"
	}
	if service != "dexmm" && service != "pushpush" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("service query parameter must be 'dexmm' or 'pushpush'"))
		return
	}
	if !s.telegramStore.Enabled() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("telegram bot token/chat_id not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	text := fmt.Sprintf("Telegram test message from %s — bot connection is working.", service)
	if err := s.telegramStore.Client().Notify(ctx, text); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func rowToResponse(row db.TelegramSettings, store *telegram.Store) telegramSettingsResponse {
	cfg := store.Get()
	dexmmWebhookURL := firstNonEmpty(row.DexmmWebhookURL, cfg.WebhookURL)
	return telegramSettingsResponse{
		BotTokenPresent:    cfg.Token != "",
		ChatID:             row.ChatID,
		DexmmWebhookURL:    dexmmWebhookURL,
		PushpushWebhookURL: row.PushpushWebhookURL,
		DexmmWebhookSet:    strings.TrimSpace(cfg.WebhookURL) != "" && store.Enabled(),
		PushpushWebhookSet: strings.TrimSpace(row.PushpushWebhookURL) != "" && store.Enabled(),
		ConfigVersion:      row.ConfigVersion,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
