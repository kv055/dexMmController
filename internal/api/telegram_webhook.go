package api

import (
	"encoding/json"
	"net/http"

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
	if s.telegramRelay == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	var u telegram.Update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.telegramRelay.HandleUpdate(r.Context(), u)
	w.WriteHeader(http.StatusOK)
}
