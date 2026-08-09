package tradechat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"commons/telegram"
	"dexMmController/internal/db"
)

type fakeChat struct {
	mu   sync.Mutex
	msgs []telegram.Message
	sent []string
}

func (f *fakeChat) SendMessage(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	f.msgs = append(f.msgs, telegram.Message{Sender: "us", Text: text})
	return nil
}

func (f *fakeChat) Messages(_ context.Context, _ string) ([]telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telegram.Message(nil), f.msgs...), nil
}

type fakeFactory struct {
	chat telegram.TradeChat
	err  error
}

func (f fakeFactory) TradeChatFor(context.Context, string) (telegram.TradeChat, error) {
	return f.chat, f.err
}

type fakeTemplates struct {
	called int
	text   string
}

func (f *fakeTemplates) GetChatTemplate(context.Context, string, string) (string, error) {
	f.called++
	return f.text, nil
}

type fakeRelay struct {
	called   int
	exchange string
	orderID  string
	outbound []string
}

func (f *fakeRelay) Register(exchange, orderID string, _ telegram.TradeChat, knownOutbound ...string) {
	f.called++
	f.exchange, f.orderID = exchange, orderID
	f.outbound = append([]string(nil), knownOutbound...)
}

func tradeTx(t *testing.T, exchange string) *db.Transaction {
	t.Helper()
	raw, err := db.MarshalDexOfferCreation(db.DexOfferMeta{Execution: map[string]any{"exchange": exchange, "trade_id": "trade-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return &db.Transaction{Dex: db.DexPlatformFromSlug(exchange), DexOfferCreation: raw, PaymentMethod: "SEPA", FiatAmount: 100, FiatCurrency: "EUR"}
}

func TestAfterFundedSendsTemplateAndRegisters(t *testing.T) {
	chat := &fakeChat{}
	templates := &fakeTemplates{text: "Please send {{amount}} {{currency}}."}
	relay := &fakeRelay{}
	client := New(fakeFactory{chat: chat}, templates, relay)

	if err := client.AfterFunded(context.Background(), tradeTx(t, "robosats")); err != nil {
		t.Fatal(err)
	}
	if len(chat.sent) != 1 || !strings.Contains(chat.sent[0], PaymentRequestMarker) {
		t.Fatalf("sent = %v", chat.sent)
	}
	if templates.called != 1 || relay.called != 1 || relay.orderID != "trade-1" || len(relay.outbound) != 1 {
		t.Fatalf("templates=%d relay=%+v", templates.called, relay)
	}
}

func TestAfterFundedHodlHodlRegistersOnly(t *testing.T) {
	chat := &fakeChat{}
	templates := &fakeTemplates{}
	relay := &fakeRelay{}
	if err := New(fakeFactory{chat: chat}, templates, relay).AfterFunded(context.Background(), tradeTx(t, "hodlhodl")); err != nil {
		t.Fatal(err)
	}
	if len(chat.sent) != 0 || templates.called != 0 || relay.called != 1 {
		t.Fatalf("sent=%v templates=%d relay=%d", chat.sent, templates.called, relay.called)
	}
}

func TestAfterFundedBisq1Unavailable(t *testing.T) {
	client := New(fakeFactory{err: ErrTradeChatUnavailable}, nil, &fakeRelay{})
	err := client.AfterFunded(context.Background(), tradeTx(t, "bisq"))
	if !errors.Is(err, ErrTradeChatUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestAfterFundedRejectsUnknownExchange(t *testing.T) {
	err := New(fakeFactory{}, nil, &fakeRelay{}).AfterFunded(context.Background(), tradeTx(t, "peachbitcoin"))
	if !errors.Is(err, ErrUnsupportedExchange) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsurePaymentRequestIgnoresUnrelatedOutbound(t *testing.T) {
	chat := &fakeChat{msgs: []telegram.Message{{Sender: "us", Outbound: true, Text: "unrelated"}}}
	if _, err := EnsurePaymentRequest(context.Background(), chat, PaymentRequest{OrderID: "trade-1"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if len(chat.sent) != 1 {
		t.Fatalf("sent = %v", chat.sent)
	}
}
