package dexnotify

import (
	"context"
	"fmt"
	"strings"

	"commons/telegram"
)

// AnomalyNotifier sends one-way Telegram alerts for structured-venue trade anomalies.
type AnomalyNotifier struct {
	tg *telegram.Client
}

// NewAnomalyNotifier creates a notifier. tg nil disables outbound alerts.
func NewAnomalyNotifier(tg *telegram.Client) *AnomalyNotifier {
	return &AnomalyNotifier{tg: tg}
}

// Enabled reports whether alerts can be sent.
func (n *AnomalyNotifier) Enabled() bool {
	return n != nil && n.tg != nil && n.tg.Enabled()
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
	return n.tg.Notify(ctx, text)
}
