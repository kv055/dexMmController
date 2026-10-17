package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"commons/telegram"
)

// WithTelegramRelay attaches the dex trade-chat relay webhook handler dependencies.
func (s *Server) WithTelegramRelay(relay *telegram.ChatRelay) *Server {
	s.telegramRelay = relay
	return s
}

func (s *Server) handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	if !telegram.WebhookAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var u telegram.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&u); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if msg := u.Message; msg != nil && msg.Chat != nil && strings.EqualFold(msg.Chat.Type, "private") && s.consumeTelegramPairing(msg.Text) {
		if err := s.pairTelegramChat(r.Context(), msg.Chat.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if s.telegramRelay != nil {
		s.telegramRelay.HandleUpdate(r.Context(), u)
	}
	w.WriteHeader(http.StatusOK)
}
