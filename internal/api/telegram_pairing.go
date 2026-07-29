package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"commons/telegram"
	"dexMmController/internal/db"
)

const telegramPairingTTL = 5 * time.Minute

type telegramPairing struct {
	nonce     string
	expiresAt time.Time
}

type telegramPairingResponse struct {
	DeepLink  string    `json:"deep_link"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleTelegramPair creates a one-time private-chat Telegram deep link. The
// caller must have already saved a bot token and public DEXMM webhook URL.
func (s *Server) handleTelegramPair(w http.ResponseWriter, r *http.Request) {
	if s.telegramSettingsRepo == nil || s.telegramStore == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("telegram settings not configured"))
		return
	}
	row, err := s.telegramSettingsRepo.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("save a Telegram bot token before pairing"))
		return
	}
	if strings.TrimSpace(row.BotToken) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("save a Telegram bot token before pairing"))
		return
	}
	webhookURL := firstNonEmpty(row.DexmmWebhookURL, s.telegramStore.Get().WebhookURL)
	if webhookURL == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("configure a public DEXMM webhook URL before pairing"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	bot, err := telegram.GetBotInfo(ctx, row.BotToken, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("validate Telegram bot token: %w", err))
		return
	}
	if err := telegram.SetWebhookWithToken(ctx, row.BotToken, webhookURL, nil); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("register Telegram webhook: %w", err))
		return
	}
	nonce, err := newTelegramPairingNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	expiresAt := time.Now().UTC().Add(telegramPairingTTL)
	s.telegramPairingMu.Lock()
	s.telegramPairing = &telegramPairing{nonce: nonce, expiresAt: expiresAt}
	s.telegramPairingMu.Unlock()
	writeJSON(w, http.StatusOK, telegramPairingResponse{
		DeepLink:  "https://t.me/" + bot.Username + "?start=connect_" + nonce,
		ExpiresAt: expiresAt,
	})
}

func newTelegramPairingNonce() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("create Telegram pairing nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Server) consumeTelegramPairing(text string) bool {
	nonce := strings.TrimPrefix(strings.TrimSpace(text), "/start connect_")
	if nonce == strings.TrimSpace(text) || nonce == "" {
		return false
	}
	s.telegramPairingMu.Lock()
	defer s.telegramPairingMu.Unlock()
	if s.telegramPairing == nil || time.Now().After(s.telegramPairing.expiresAt) || nonce != s.telegramPairing.nonce {
		return false
	}
	s.telegramPairing = nil
	return true
}

func (s *Server) pairTelegramChat(ctx context.Context, chatID int64) error {
	if chatID == 0 || s.telegramSettingsRepo == nil {
		return fmt.Errorf("Telegram pairing is unavailable")
	}
	id := fmt.Sprintf("%d", chatID)
	updated, err := s.telegramSettingsRepo.Update(ctx, db.TelegramSettingsUpdate{ChatID: &id})
	if err != nil {
		return err
	}
	s.applyTelegramConfig(updated)
	if client := s.telegramStore.Client(); client != nil {
		_ = client.Notify(ctx, "Telegram connected. Test messages and trading alerts will be sent here.")
	}
	return nil
}
