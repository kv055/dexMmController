package dexnotify

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"commons/telegram"
)

// AnomalyNotifier sends one-way Telegram alerts for structured-venue trade anomalies.
// The outbound client is mutable at runtime via SetClient so hot-reloaded bot
// credentials propagate without a restart.
type AnomalyNotifier struct {
	mu sync.RWMutex
	tg *telegram.Client
}

// NewAnomalyNotifier creates a notifier. tg nil disables outbound alerts.
func NewAnomalyNotifier(tg *telegram.Client) *AnomalyNotifier {
	return &AnomalyNotifier{tg: tg}
}

// SetClient swaps the outbound Telegram client. Pass nil to disable alerts.
func (n *AnomalyNotifier) SetClient(tg *telegram.Client) {
	n.mu.Lock()
	n.tg = tg
	n.mu.Unlock()
}

func (n *AnomalyNotifier) client() *telegram.Client {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.tg
}

// Enabled reports whether alerts can be sent.
func (n *AnomalyNotifier) Enabled() bool {
	c := n.client()
	return n != nil && c != nil && c.Enabled()
}

// NotifyAnomaly relays a structured-venue anomaly to the operator.
func (n *AnomalyNotifier) NotifyAnomaly(ctx context.Context, exchange, tradeID, reason, detail string) error {
	if !n.Enabled() {
		return nil
	}
	text := fmt.Sprintf("Trade anomaly [%s #%s]\n%s", exchange, tradeID, strings.TrimSpace(reason))
	if d := strings.TrimSpace(detail); d != "" {
		text += "\n" + d
	}
	return n.client().Notify(ctx, text)
}
