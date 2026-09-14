package mostro

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"commons/credentialcrypto"
	lnd "dexMmController/internal/wallethub"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

type memoryStore struct {
	data        []byte
	cipher      []byte
	allocations int
	out         map[string]nostr.Event
	authorized  bool
	saves       int
}

func (s *memoryStore) LockMostro(context.Context, uint64) (func(), error) { return func() {}, nil }
func (s *memoryStore) LoadMostroSession(context.Context, uint64) (*Session, error) {
	if s.data == nil {
		return nil, nil
	}
	var v Session
	err := json.Unmarshal(s.data, &v)
	v.EncryptedMnemonic = s.cipher
	return &v, err
}
func (s *memoryStore) CreateMostroSession(ctx context.Context, _ string, cipher []byte, build func([]byte, int64) (*Session, error)) (*Session, error) {
	s.allocations++
	v, err := build(cipher, int64(s.allocations))
	if err != nil {
		return nil, err
	}
	v.ID = 1
	v.IdentityID = 1
	v.TradeIndex = int64(s.allocations)
	v.EncryptedMnemonic = cipher
	s.cipher = cipher
	return v, s.SaveMostroSession(ctx, v, nil)
}
func (s *memoryStore) SaveMostroSession(_ context.Context, v *Session, _ *nostr.Event) error {
	s.saves++
	data, err := json.Marshal(v)
	s.data = data
	return err
}
func (s *memoryStore) MostroOutbox(_ context.Context, _ int64, action string, build func() (nostr.Event, error)) (nostr.Event, error) {
	if e, ok := s.out[action]; ok {
		return e, nil
	}
	e, err := build()
	if err == nil {
		s.out[action] = e
	}
	return e, err
}
func (s *memoryStore) AckMostroOutbox(context.Context, string) error { return nil }
func (s *memoryStore) AuthorizeMostroRelease(context.Context, uint64) error {
	if !s.authorized {
		return errors.New("fiat receipt missing")
	}
	return nil
}
func (s *memoryStore) LoadMostroChatEvents(context.Context, int64) ([]nostr.Event, error) {
	return nil, nil
}
func (s *memoryStore) SaveMostroChatEvent(context.Context, int64, nostr.Event, string) error {
	return nil
}

type simulatedNode struct {
	key         string
	node, order nostr.Event
	conn        *simulatedConnection
	publishIDs  []string
	failPublish bool
	onPublish   func(nostr.Event)
	closed      int
	history     []nostr.Event
	openError   error
}
type simulatedConnection struct {
	node   *simulatedNode
	events chan nostr.Event
}

func (n *simulatedNode) Query(_ context.Context, f nostr.Filter) ([]nostr.Event, error) {
	if f.Kinds[0] == 38385 {
		return []nostr.Event{n.node}, nil
	}
	if f.Kinds[0] == 38383 {
		return []nostr.Event{n.order}, nil
	}
	return n.history, nil
}
func (n *simulatedNode) Open(context.Context, nostr.Filter) (Connection, error) {
	if n.openError != nil {
		return nil, n.openError
	}
	c := &simulatedConnection{node: n, events: make(chan nostr.Event, 30)}
	n.conn = c
	return c, nil
}
func (c *simulatedConnection) History() []nostr.Event {
	return append([]nostr.Event(nil), c.node.history...)
}
func (c *simulatedConnection) Events() <-chan nostr.Event { return c.events }
func (c *simulatedConnection) Close()                     { c.node.closed++ }
func (c *simulatedConnection) Publish(_ context.Context, e nostr.Event) error {
	c.node.publishIDs = append(c.node.publishIDs, e.ID)
	if c.node.failPublish {
		return errors.New("lost relay acknowledgement")
	}
	if c.node.onPublish != nil {
		c.node.onPublish(e)
	}
	return nil
}
func (n *simulatedNode) send(t *testing.T, recipient string, r TakeRequest, action string, payload json.RawMessage) {
	t.Helper()
	raw, _ := json.Marshal([]any{OrderMessage{MessageKind{Version: r.Protocol, ID: r.OrderID, RequestID: &r.RequestID, Action: action, Payload: payload}}, nil, nil})
	key, _ := nip44.GenerateConversationKey(recipient, n.key)
	cipher, err := nip44.Encrypt(string(raw), key)
	if err != nil {
		t.Fatal(err)
	}
	e := nostr.Event{Kind: 14, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", recipient}}, Content: cipher}
	if err = e.Sign(n.key); err != nil {
		t.Fatal(err)
	}
	n.conn.events <- e
}

type simulatedWallet struct {
	statuses map[string]string
	decoded  map[string]*lnd.DecodedInvoice
	calls    []string
	onPay    func(string)
}

func (w *simulatedWallet) DecodeInvoice(_ context.Context, i string) (*lnd.DecodedInvoice, error) {
	d := w.decoded[i]
	if d == nil {
		return nil, errors.New("invalid invoice")
	}
	return d, nil
}
func (w *simulatedWallet) TrackHoldPayment(_ context.Context, h string) (string, error) {
	if s := w.statuses[h]; s != "" {
		return s, nil
	}
	return "NOT_FOUND", nil
}
func (w *simulatedWallet) HoldPayment(_ context.Context, i, h string, _ int64) (string, error) {
	if s := w.statuses[h]; s != "" {
		return s, nil
	}
	w.calls = append(w.calls, h)
	w.statuses[h] = "IN_FLIGHT"
	if w.onPay != nil {
		w.onPay(i)
	}
	return "IN_FLIGHT", nil
}
func fixture(t *testing.T, bond bool) (*Manager, *memoryStore, *simulatedNode, *simulatedWallet, TakeRequest) {
	t.Helper()
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cipher, err := credentialcrypto.Encrypt(reviewMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	key := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(key)
	orderID := "308e1272-d5f4-47e6-bd97-3504baea9c23"
	node := nostr.Event{Kind: 38385, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"d", pub}, {"protocol_version", "2"}, {"y", "mostro"}, {"lnd_networks", "mainnet"}, {"lnd_node_pubkey", "02" + pub}, {"fee", "0.003"}, {"bond_enabled", fmt.Sprint(bond)}, {"bond_apply_to", "take"}, {"bond_amount_pct", "0.01"}, {"bond_base_amount_sats", "500"}}}
	_ = node.Sign(key)
	order := nostr.Event{Kind: 38383, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"d", orderID}, {"s", "pending"}, {"k", "buy"}, {"y", "mostro"}, {"network", "mainnet"}, {"layer", "lightning"}, {"f", "EUR"}, {"fa", "50"}, {"amt", "9985"}, {"pm", "SEPA"}}}
	_ = order.Sign(key)
	r := TakeRequest{RequestID: 42, Node: pub, OrderID: orderID, SourceEventID: order.ID, Protocol: 2, FiatAmount: 50, FiatCurrency: "EUR", PaymentMethod: "SEPA", MaxEscrowSat: 10000, MaxBondSat: 500, RoutingFeeSat: 20}
	n := &simulatedNode{key: key, node: node, order: order}
	s := &memoryStore{out: map[string]nostr.Event{}}
	w := &simulatedWallet{statuses: map[string]string{}, decoded: map[string]*lnd.DecodedInvoice{}}
	for _, purpose := range []string{"bond", "escrow"} {
		amount := "10000"
		hash := strings.Repeat("a", 64)
		if purpose == "bond" {
			amount = "500"
			hash = strings.Repeat("b", 64)
		}
		w.decoded["lnbc10"+purpose] = &lnd.DecodedInvoice{Destination: "02" + pub, PaymentHash: hash, NumSatoshis: amount, Timestamp: fmt.Sprint(time.Now().Unix()), Expiry: "3600"}
	}
	m := &Manager{Mnemonic: reviewMnemonic, IdentityLabel: "test", EncryptedMnemonic: []byte(cipher), Store: s, Transport: n, Wallet: w, AllowedNodes: []string{pub}, Enabled: true}
	return m, s, n, w, r
}
func invoicePayload(purpose string) json.RawMessage {
	return encodePayload(map[string]any{"payment_request": []any{nil, "lnbc10" + purpose, nil}})
}
func TestTakeAndReleaseReconcileBondAndEscrow(t *testing.T) {
	m, s, n, w, r := fixture(t, true)
	var recipient string
	n.onPublish = func(e nostr.Event) {
		recipient = e.PubKey
		k, _ := nip44.GenerateConversationKey(e.PubKey, n.key)
		raw, err := nip44.Decrypt(e.Content, k)
		if err != nil {
			t.Fatal(err)
		}
		var tuple []json.RawMessage
		_ = json.Unmarshal([]byte(raw), &tuple)
		var msg OrderMessage
		_ = json.Unmarshal(tuple[0], &msg)
		switch msg.Order.Action {
		case "take-buy":
			if string(msg.Order.Payload) != `{"amount":50}` {
				t.Fatalf("wrong selected amount: %s", msg.Order.Payload)
			}
			n.send(t, recipient, r, "pay-bond-invoice", invoicePayload("bond"))
		case "release":
			w.statuses[strings.Repeat("a", 64)] = "SUCCEEDED"
			w.statuses[strings.Repeat("b", 64)] = "FAILED"
			n.send(t, recipient, r, "released", json.RawMessage("null"))
		}
	}
	w.onPay = func(invoice string) {
		if invoice == "lnbc10bond" {
			n.send(t, recipient, r, "pay-invoice", invoicePayload("escrow"))
		} else {
			n.send(t, recipient, r, "hold-invoice-payment-accepted", json.RawMessage("null"))
		}
	}
	result, err := m.Take(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Funded || len(w.calls) != 2 || w.calls[0] != strings.Repeat("b", 64) {
		t.Fatalf("funding order incorrect: %+v %+v", result, w.calls)
	}
	before := len(n.publishIDs)
	if err = m.Release(context.Background(), r.RequestID, r.OrderID); err == nil {
		t.Fatal("release allowed without fiat receipt")
	}
	if len(n.publishIDs) != before {
		t.Fatal("unauthorized release published")
	}
	s.authorized = true
	if err = m.Release(context.Background(), r.RequestID, r.OrderID); err != nil {
		t.Fatal(err)
	}
	before = len(n.publishIDs)
	if err = m.Release(context.Background(), r.RequestID, r.OrderID); err != nil {
		t.Fatal(err)
	}
	if len(n.publishIDs) != before {
		t.Fatal("settled release was republished")
	}
	if n.closed != 3 {
		t.Fatalf("connection cleanup count=%d", n.closed)
	}
}
func TestRetryAfterLostPublishRestoresExactKeyAndEvent(t *testing.T) {
	m, s, n, _, r := fixture(t, false)
	n.failPublish = true
	if _, err := m.Take(context.Background(), r); err == nil {
		t.Fatal("expected publish failure")
	}
	// Simulate a restart: recover the mnemonic from the store, not environment.
	restarted := *m
	restarted.Mnemonic = ""
	restarted.EncryptedMnemonic = nil
	if _, err := restarted.Take(context.Background(), r); err == nil {
		t.Fatal("expected publish failure")
	}
	if s.allocations != 1 || len(n.publishIDs) != 2 || n.publishIDs[0] != n.publishIDs[1] {
		t.Fatalf("retry changed session or request: %d %v", s.allocations, n.publishIDs)
	}
	r.FiatAmount++
	if _, err := restarted.Take(context.Background(), r); err == nil {
		t.Fatal("changed retry terms accepted")
	}
	if len(n.publishIDs) != 2 {
		t.Fatal("changed retry terms published")
	}
}
func TestInFlightDoesNotMeanEscrowAccepted(t *testing.T) {
	m, _, n, w, r := fixture(t, false)
	n.onPublish = func(e nostr.Event) { n.send(t, e.PubKey, r, "pay-invoice", invoicePayload("escrow")) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.onPay = func(string) { cancel() }
	if _, err := m.Take(ctx, r); err == nil {
		t.Fatal("IN_FLIGHT counted as daemon acceptance")
	}
	if len(w.calls) != 1 {
		t.Fatalf("payment calls=%d", len(w.calls))
	}
}
func TestPreflightRejectsChangedQuoteAndMissingBondBudgetBeforeAllocation(t *testing.T) {
	for _, reason := range []string{"allowlist", "quote", "bond", "amount"} {
		t.Run(reason, func(t *testing.T) {
			m, s, n, w, r := fixture(t, true)
			switch reason {
			case "allowlist":
				m.AllowedNodes = nil
			case "quote":
				r.SourceEventID = strings.Repeat("0", 64)
			case "bond":
				r.MaxBondSat = 0
			case "amount":
				r.FiatAmount = 101
			}
			if _, err := m.Take(context.Background(), r); err == nil {
				t.Fatal("unsafe request accepted")
			}
			if s.allocations != 0 || len(n.publishIDs) > 0 || len(w.calls) > 0 {
				t.Fatal("preflight had side effects")
			}
		})
	}
}
func TestTypedNilManagerIsDisabled(t *testing.T) {
	var m *Manager
	if m.Ready() == nil {
		t.Fatal("nil manager ready")
	}
	if _, err := m.Take(context.Background(), TakeRequest{}); err == nil {
		t.Fatal("nil manager took offer")
	}
}
