package dexaccount

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStyleFor(t *testing.T) {
	cases := map[string]paymentStyle{
		"bisq":     styleStructured,
		"robosats": styleChat,
		"bisq2":    styleChat,
		"hodlhodl": styleStructured,
		"":         styleUnknown,
	}
	for exchange, want := range cases {
		if got := styleFor(exchange); got != want {
			t.Errorf("styleFor(%q) = %v, want %v", exchange, got, want)
		}
	}
}

func TestIsStructuredVenue(t *testing.T) {
	r := NewPaymentResolver(nil, nil, nil, nil, nil)
	if !r.IsStructuredVenue("bisq") {
		t.Error("bisq should be structured")
	}
	if r.IsStructuredVenue("robosats") {
		t.Error("robosats should not be structured (it's a chat venue)")
	}
}

func TestFetchCounterpartyRouting(t *testing.T) {
	r := NewPaymentResolver(nil, nil, nil, nil, nil)
	// Chat venue must not use the one-shot structured fetch.
	if _, err := r.FetchCounterparty(context.Background(), "robosats", map[string]any{"position_id": "p1"}); err == nil {
		t.Fatal("expected robosats to be rejected from FetchCounterparty (chat flow)")
	}
	// HodlHodl is structured (contract holds payment details); chat is relay-only.
	if _, err := r.FetchCounterparty(context.Background(), "hodlhodl", map[string]any{"trade_id": "t1"}); err == nil {
		t.Fatal("expected error when bisq client is not configured for hodlhodl structured fetch")
	}
	// Structured venue with no client configured.
	if _, err := r.FetchCounterparty(context.Background(), "bisq", map[string]any{"trade_id": "t1"}); err == nil {
		t.Fatal("expected error when bisq client is not configured")
	}
}

func TestPaymentRequestMessage(t *testing.T) {
	msg := paymentRequestMessage("", "100.00", "EUR")
	if !strings.Contains(msg, "100.00 EUR") {
		t.Errorf("expected amount+currency in message, got: %q", msg)
	}
	if !strings.Contains(msg, paymentRequestMarker) {
		t.Errorf("template must contain the idempotency marker %q", paymentRequestMarker)
	}
	// Empty amount falls back to a readable phrase, no leftover formatting artifacts.
	if fb := paymentRequestMessage("", "", ""); !strings.Contains(fb, "the agreed amount") {
		t.Errorf("expected fallback phrase, got: %q", fb)
	}
}

func TestPaymentRequestMessageCustomTemplate(t *testing.T) {
	custom := "Please send {{amount}} {{currency}} via your preferred method."
	msg := paymentRequestMessage(custom, "100.00", "EUR")
	if msg != "Please send 100.00 EUR via your preferred method." {
		t.Errorf("unexpected rendered message: %q", msg)
	}
	// Empty placeholders fall back to the agreed-amount phrase; whitespace runs are collapsed.
	msg = paymentRequestMessage(custom, "", "")
	if msg != "Please send the agreed amount via your preferred method." {
		t.Errorf("unexpected fallback message: %q", msg)
	}
}

// fakeChat is an in-memory TradeChat for testing the conversation core.
type fakeChat struct {
	msgs []TradeChatMessage
	sent []string
}

func (f *fakeChat) SendMessage(_ context.Context, _ string, text string) error {
	f.sent = append(f.sent, text)
	// Echo our own message back into the thread, as the real chat would.
	f.msgs = append(f.msgs, TradeChatMessage{Sender: "us", Outbound: true, Text: text})
	return nil
}

func (f *fakeChat) Messages(_ context.Context, _ string) ([]TradeChatMessage, error) {
	return f.msgs, nil
}

func TestCaptureFromChatSendsRequestOnce(t *testing.T) {
	ctx := context.Background()
	req := ChatRequest{Exchange: "robosats", OrderID: "o1", ExpectedAmount: "100.00", ExpectedCurrency: "EUR"}

	chat := &fakeChat{}
	if _, err := captureFromChat(ctx, chat, req, ""); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if len(chat.sent) != 1 {
		t.Fatalf("expected exactly one request sent, got %d", len(chat.sent))
	}
	// A second pass must NOT resend (idempotent via the request marker already in-thread).
	if _, err := captureFromChat(ctx, chat, req, ""); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if len(chat.sent) != 1 {
		t.Fatalf("expected no resend on second pass, got %d total", len(chat.sent))
	}
}

func TestCaptureFromChatInboundOnly(t *testing.T) {
	ctx := context.Background()
	req := ChatRequest{Exchange: "robosats", OrderID: "o1", ExpectedAmount: "100.00", ExpectedCurrency: "EUR"}

	// Thread already has our request (marker) + the counterparty's reply.
	chat := &fakeChat{msgs: []TradeChatMessage{
		{Sender: "us", Outbound: true, Text: paymentRequestMessage("", "100.00", "EUR")},
		{Sender: "Robo123", Time: "2026-06-29T10:00:00Z", Text: "IBAN DE89..., holder Max"},
	}}

	out, err := captureFromChat(ctx, chat, req, "")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(chat.sent) != 0 {
		t.Fatalf("request already present; should not resend, sent %d", len(chat.sent))
	}

	var env chatPaymentEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Source != "chat" || env.Exchange != "robosats" || env.OrderID != "o1" {
		t.Fatalf("unexpected envelope header: %+v", env)
	}
	if env.Expected.Amount != "100.00" || env.Expected.Currency != "EUR" {
		t.Fatalf("unexpected expected: %+v", env.Expected)
	}
	if len(env.CounterpartyMessages) != 1 {
		t.Fatalf("expected exactly the 1 inbound message (our outbound dropped), got %d: %+v", len(env.CounterpartyMessages), env.CounterpartyMessages)
	}
	if env.CounterpartyMessages[0].Text != "IBAN DE89..., holder Max" {
		t.Fatalf("wrong inbound text: %q", env.CounterpartyMessages[0].Text)
	}
	if env.CounterpartyHandle != "Robo123" {
		t.Fatalf("expected counterparty handle Robo123, got %q", env.CounterpartyHandle)
	}
}

func TestCaptureFromChatUsesCustomTemplate(t *testing.T) {
	ctx := context.Background()
	req := ChatRequest{
		Exchange:         "robosats",
		OrderID:          "o1",
		ExpectedAmount:   "100.00",
		ExpectedCurrency: "EUR",
		PaymentMethod:    "Revolut",
	}
	custom := "Hi, please send {{amount}} {{currency}} via Revolut and reply with your handle."

	chat := &fakeChat{}
	if _, err := captureFromChat(ctx, chat, req, custom); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(chat.sent) != 1 {
		t.Fatalf("expected exactly one request sent, got %d", len(chat.sent))
	}
	want := "Hi, please send 100.00 EUR via Revolut and reply with your handle."
	if chat.sent[0] != want {
		t.Fatalf("unexpected sent message: %q, want %q", chat.sent[0], want)
	}
}
