package robosats

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	lnd "dexTakerController/internal/wallethub"
)

type memoryStore struct {
	mu   sync.Mutex
	data []byte
	id   int64
}

func (s *memoryStore) LockRoboSats(context.Context, uint64) (func(), error) { return func() {}, nil }
func (s *memoryStore) LoadRoboSatsSession(_ context.Context, id uint64) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return nil, nil
	}
	var v Session
	if err := json.Unmarshal(s.data, &v); err != nil {
		return nil, err
	}
	if v.Request.RequestID != id {
		return nil, fmt.Errorf("id mismatch")
	}
	return &v, nil
}
func (s *memoryStore) CreateRoboSatsSession(_ context.Context, sess *Session) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id++
	sess.ID = s.id
	if sess.Payments == nil {
		sess.Payments = map[string]*Payment{}
	}
	data, err := json.Marshal(sess)
	s.data = data
	return sess, err
}
func (s *memoryStore) SaveRoboSatsSession(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(sess)
	s.data = data
	return err
}

type fakeWallet struct {
	decoded map[string]*lnd.DecodedInvoice
	status  map[string]string
	holds   []string
}

func (w *fakeWallet) DecodeInvoice(_ context.Context, invoice string) (*lnd.DecodedInvoice, error) {
	if d, ok := w.decoded[invoice]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unknown invoice")
}
func (w *fakeWallet) HoldPayment(_ context.Context, invoice, hash string, _ int64) (string, error) {
	w.holds = append(w.holds, invoice+":"+hash)
	if st, ok := w.status[hash]; ok {
		return st, nil
	}
	return "IN_FLIGHT", nil
}
func (w *fakeWallet) TrackHoldPayment(_ context.Context, hash string) (string, error) {
	if st, ok := w.status[hash]; ok {
		return st, nil
	}
	return "IN_FLIGHT", nil
}

type fakeOrderAPI struct {
	robot     Robot
	positions map[string]*Position
	takeBody  map[string]any
	chats     *ChatThread
}

func (a *fakeOrderAPI) GetRobot(context.Context) (Robot, error) { return a.robot, nil }
func (a *fakeOrderAPI) TakeOfferWithAmount(_ context.Context, id, amount string) (*Position, error) {
	a.takeBody = map[string]any{"id": id, "amount": amount}
	p := a.positions[id]
	if p == nil {
		return nil, fmt.Errorf("missing")
	}
	cp := *p
	cp.IsTaker = true
	cp.IsSeller = true
	cp.Status = StatusWaitingTakerBond
	a.positions[id] = &cp
	return &cp, nil
}
func (a *fakeOrderAPI) GetPosition(_ context.Context, id string) (*Position, error) {
	p := a.positions[id]
	if p == nil {
		return nil, fmt.Errorf("missing")
	}
	cp := *p
	return &cp, nil
}
func (a *fakeOrderAPI) GetChatMessages(context.Context, string) (*ChatThread, error) {
	return a.chats, nil
}
func (a *fakeOrderAPI) SendChatMessage(context.Context, string, string) error { return nil }
func (a *fakeOrderAPI) Identity() *Identity {
	return &Identity{PublicKeyArmored: "pub", EncPrivKeyArmored: "priv"}
}

func decoded(hash string, sat int64) *lnd.DecodedInvoice {
	return &lnd.DecodedInvoice{
		Destination:  strings.Repeat("a", 66),
		PaymentHash:  hash,
		NumSatoshis:  fmt.Sprintf("%d", sat),
		NumMSatoshis: fmt.Sprintf("%d", sat*1000),
		Timestamp:    fmt.Sprintf("%d", time.Now().Add(-time.Minute).Unix()),
		Expiry:       "3600",
	}
}

func TestCoordinatorPaysBondThenEscrowWithoutDuplicate(t *testing.T) {
	bondHash := hex.EncodeToString(bytesN(32, 1))
	escrowHash := hex.EncodeToString(bytesN(32, 2))
	api := &fakeOrderAPI{
		robot: map[string]any{"public_key": "pub", "encrypted_private_key": "priv"},
		positions: map[string]*Position{
			"42": {
				ID: "42", Type: TypeBuy, Status: StatusWaitingTakerBond, IsTaker: true, IsSeller: true,
				BondInvoice: "lnbc1bond", PaymentMethod: "SEPA",
			},
		},
	}
	wallet := &fakeWallet{
		decoded: map[string]*lnd.DecodedInvoice{
			"lnbc1bond":   decoded(bondHash, 10_000),
			"lnbc1escrow": decoded(escrowHash, 90_000),
		},
		status: map[string]string{bondHash: "IN_FLIGHT", escrowHash: "IN_FLIGHT"},
	}
	store := &memoryStore{}
	steps := 0
	coord := &Coordinator{
		Enabled: true, Store: store, Wallet: wallet,
		Registry: map[string]string{"expats": "http://coord.test"},
		Dial:     func(string, string) (OrderAPI, error) { return api, nil },
		Token:    func(context.Context, int64) (string, error) { return "robot-token", nil },
		Sleep: func(ctx context.Context, _ time.Duration) error {
			steps++
			p := api.positions["42"]
			if steps == 1 {
				p.TakerLocked = true
				p.BondInvoice = ""
				p.Status = StatusWaitingSellerEscrow
				p.EscrowInvoice = "lnbc1escrow"
			}
			if steps == 2 {
				p.EscrowLocked = true
				p.Status = StatusSendingFiat
			}
			return nil
		},
	}
	req := TakeRequest{RequestID: 7, CoordinatorID: "expats", OrderID: "42", FiatAmount: "100.00", FiatCurrency: "EUR", PaymentMethod: "SEPA", MaxBondSat: 20_000, MaxEscrowSat: 100_000, RoutingFeeSat: 10, CredentialID: 3}
	got, err := coord.Take(context.Background(), req)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if !got.Funded || got.Phase != PhaseChatReady {
		t.Fatalf("result=%+v", got)
	}
	if len(wallet.holds) != 2 {
		t.Fatalf("holds=%v, want bond then escrow", wallet.holds)
	}
	got2, err := coord.Take(context.Background(), req)
	if err != nil {
		t.Fatalf("resume Take: %v", err)
	}
	if len(wallet.holds) != 2 {
		t.Fatalf("duplicate payment on resume: %v", wallet.holds)
	}
	if !got2.Funded {
		t.Fatal("resume lost funded state")
	}
}

func TestCoordinatorDoesNotRaiseEscrowCap(t *testing.T) {
	api := &fakeOrderAPI{
		robot: map[string]any{},
		positions: map[string]*Position{
			"9": {ID: "9", Type: TypeBuy, Status: StatusWaitingSellerEscrow, IsTaker: true, IsSeller: true, TakerLocked: true, EscrowInvoice: "lnbc1big", PaymentMethod: "SEPA"},
		},
	}
	wallet := &fakeWallet{decoded: map[string]*lnd.DecodedInvoice{"lnbc1big": decoded(hex.EncodeToString(bytesN(32, 4)), 200_000)}}
	coord := &Coordinator{
		Enabled: true, Store: &memoryStore{}, Wallet: wallet,
		Registry: map[string]string{"expats": "http://coord.test"},
		Dial:     func(string, string) (OrderAPI, error) { return api, nil },
		Token:    func(context.Context, int64) (string, error) { return "token", nil },
		Sleep:    func(context.Context, time.Duration) error { return context.Canceled },
	}
	req := TakeRequest{RequestID: 1, CoordinatorID: "expats", OrderID: "9", FiatAmount: "100", FiatCurrency: "EUR", PaymentMethod: "SEPA", MaxBondSat: 20_000, MaxEscrowSat: 100_000, RoutingFeeSat: 10, CredentialID: 1}
	_, err := coord.Store.CreateRoboSatsSession(context.Background(), &Session{Request: req, Phase: PhaseBondLocked, Payments: map[string]*Payment{"bond": {Purpose: "bond", Hash: "bond", Submitted: true, Status: "IN_FLIGHT"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coord.Take(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "exceeds confirmed cap") {
		t.Fatalf("err=%v, want cap failure", err)
	}
	if len(wallet.holds) != 0 {
		t.Fatalf("paid over-cap escrow: %v", wallet.holds)
	}
}

func TestCoordinatorRejectsPeerTemplateAsOwn(t *testing.T) {
	template := "Please send 100 EUR\n\n[dexmm-payment-request:v1]"
	thread := &ChatThread{Messages: []ChatMessage{{Nick: "peer", Plaintext: template}}}
	if ownMessageContains(thread, template, "me") {
		t.Fatal("peer template treated as own")
	}
	own := &ChatThread{Messages: []ChatMessage{{Nick: "me", Plaintext: template, Own: true}}}
	if !ownMessageContains(own, template, "me") {
		t.Fatal("own template not recognized")
	}
}

func bytesN(n, fill byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = fill
	}
	return b
}
