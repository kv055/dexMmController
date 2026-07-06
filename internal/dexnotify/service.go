package dexnotify

import (
	"context"
	"fmt"
	"strings"

	"commons/telegram"
	"dexMmController/internal/dexaccount"
)

// Service wires trade-chat sessions into the shared Telegram relay.
type Service struct {
	relay    *telegram.ChatRelay
	resolver *dexaccount.PaymentResolver
}

// NewService creates a dexnotify service. Either argument may be nil to disable that side.
func NewService(relay *telegram.ChatRelay, resolver *dexaccount.PaymentResolver) *Service {
	return &Service{relay: relay, resolver: resolver}
}

// Enabled reports whether Telegram relay registration is available.
func (s *Service) Enabled() bool {
	return s != nil && s.relay != nil && s.resolver != nil
}

// RegisterTradeChat resolves the venue TradeChat transport and tracks it in the relay.
func (s *Service) RegisterTradeChat(ctx context.Context, exchange, orderID string) error {
	if !s.Enabled() {
		return nil
	}
	exchange = strings.TrimSpace(exchange)
	orderID = strings.TrimSpace(orderID)
	if exchange == "" || orderID == "" {
		return nil
	}
	chat, err := s.resolver.TradeChatFor(ctx, exchange)
	if err != nil {
		return fmt.Errorf("dexnotify: register trade chat: %w", err)
	}
	s.relay.Register(exchange, orderID, chat)
	return nil
}

// Relay exposes the underlying ChatRelay (for webhook handling).
func (s *Service) Relay() *telegram.ChatRelay {
	if s == nil {
		return nil
	}
	return s.relay
}
