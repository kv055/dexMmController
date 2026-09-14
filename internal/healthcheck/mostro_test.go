package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"commons/telegram"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
)

type fakeMostroReconciler struct {
	complete bool
	err      error
	id       uint64
	ref      string
}

func (m *fakeMostroReconciler) Reconcile(_ context.Context, id uint64, ref string) (bool, error) {
	m.id, m.ref = id, ref
	return m.complete, m.err
}

type fakeMostroChat struct {
	ref  string
	sent []string
	msgs []telegram.Message
}

func (c *fakeMostroChat) Messages(_ context.Context, ref string) ([]telegram.Message, error) {
	c.ref = ref
	return c.msgs, nil
}
func (c *fakeMostroChat) SendMessage(_ context.Context, ref, text string) error {
	c.ref = ref
	c.sent = append(c.sent, text)
	c.msgs = append(c.msgs, telegram.Message{Outbound: true, Text: text})
	return nil
}

type fakeMostroChatRelay struct {
	exchange, ref string
	calls         int
}

func (r *fakeMostroChatRelay) RegisterTradeChat(_ context.Context, exchange, ref string) error {
	r.exchange, r.ref = exchange, ref
	r.calls++
	return nil
}
func mostroTransaction() *db.Transaction {
	return &db.Transaction{ID: 42, Dex: db.DexMostro, Status: db.StatusDexPositionTaken, Direction: "bid", FiatAmount: 50, FiatCurrency: "EUR", PaymentMethod: "SEPA", DexOfferCreation: json.RawMessage(`{"execution":{"exchange":"mostro","mostro_trade_ref":"node:order:42"}}`)}
}
func TestMostroFundedHealthcheckCapturesAndRegistersChat(t *testing.T) {
	store := &fakeTransactionStore{}
	m := &fakeMostroReconciler{}
	relay := &fakeMostroChatRelay{}
	chat := &fakeMostroChat{msgs: []telegram.Message{{Sender: "peer", Text: "Sender: Test Buyer, account ending 1234"}}}
	resolver := (&dexaccount.PaymentResolver{}).WithMostro(chat)
	h := (&HealthChecker{repo: store, telegram: relay}).WithMostro(m).WithPaymentResolver(resolver)
	tx := mostroTransaction()
	for i := 0; i < 2; i++ {
		if err := h.verify(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
	}
	if m.id != 42 || m.ref != "node:order:42" || chat.ref != m.ref {
		t.Fatal("wrong transaction or chat reference")
	}
	if len(chat.sent) != 1 || !strings.Contains(chat.sent[0], "50.00 EUR") {
		t.Fatalf("payment request: %v", chat.sent)
	}
	if store.updates != 2 || !strings.Contains(string(tx.CounterpartyPaymentInfo), "Test Buyer") {
		t.Fatal("counterparty payment details not saved")
	}
	if relay.calls != 2 || relay.exchange != "mostro" || relay.ref != m.ref {
		t.Fatal("chat not registered")
	}
	if tx.TransactionComplete {
		t.Fatal("funded trade marked complete")
	}
}
func TestMostroHealthcheckCompletionAndReviewGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete bool
		err      error
	}{
		{"pending", false, nil}, {"settled", true, nil}, {"review", false, errors.New("trade requires review")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeTransactionStore{}
			m := &fakeMostroReconciler{complete: tc.complete, err: tc.err}
			h := (&HealthChecker{repo: store}).WithMostro(m)
			tx := mostroTransaction()
			tx.Status = db.StatusDexPositionReleased
			err := h.verify(context.Background(), tx)
			if (err != nil) != (tc.err != nil) {
				t.Fatalf("error=%v", err)
			}
			if tx.TransactionComplete != tc.complete {
				t.Fatalf("complete=%v", tx.TransactionComplete)
			}
			want := 0
			if tc.complete {
				want = 1
			}
			if store.updates != want {
				t.Fatalf("updates=%d", store.updates)
			}
		})
	}
	t.Run("review blocks chat", func(t *testing.T) {
		chat := &fakeMostroChat{}
		store := &fakeTransactionStore{}
		h := (&HealthChecker{repo: store}).WithMostro(&fakeMostroReconciler{err: errors.New("disputed")}).WithPaymentResolver((&dexaccount.PaymentResolver{}).WithMostro(chat))
		if err := h.verify(context.Background(), mostroTransaction()); err == nil {
			t.Fatal("review ignored")
		}
		if len(chat.sent) != 0 || store.updates != 0 {
			t.Fatal("review started payment conversation")
		}
	})
}
