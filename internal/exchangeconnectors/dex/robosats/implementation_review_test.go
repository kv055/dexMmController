package robosats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func reviewRequest() TakeRequest {
	return TakeRequest{RequestID: 7, CoordinatorID: "expats", OrderID: "42", FiatAmount: "100", FiatCurrency: "EUR", PaymentMethod: "SEPA", MaxBondSat: 20000, MaxEscrowSat: 100000, RoutingFeeSat: 10, CredentialID: 3}
}

func reviewCoordinator(api OrderAPI, wallet Wallet, store Store) *Coordinator {
	return &Coordinator{Enabled: true, Store: store, Wallet: wallet, Registry: map[string]string{"expats": "http://coord.test"}, Dial: func(string, string) (OrderAPI, error) { return api, nil }, Token: func(context.Context, int64) (string, error) { return "token", nil }}
}

type reviewFeeWallet struct {
	fakeWallet
	fees []int64
}

func (w *reviewFeeWallet) HoldPayment(ctx context.Context, invoice, hash string, fee int64) (string, error) {
	w.fees = append(w.fees, fee)
	return w.fakeWallet.HoldPayment(ctx, invoice, hash, fee)
}

func TestImplementationReviewSequentialFeeBudgets(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	w := &reviewFeeWallet{}
	c := reviewCoordinator(&fakeOrderAPI{}, w, store)
	s, _ := store.CreateRoboSatsSession(ctx, &Session{Request: reviewRequest(), Payments: map[string]*Payment{"bond": {Invoice: "bond", Hash: "bond", Status: "requested"}}})
	if err := c.submitPending(ctx, s); err != nil {
		t.Fatal(err)
	}
	s.Payments["escrow"] = &Payment{Invoice: "escrow", Hash: "escrow", Status: "requested"}
	if err := c.submitPending(ctx, s); err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, fee := range w.fees {
		sum += fee
	}
	if sum > s.Request.RoutingFeeSat {
		t.Fatalf("per-payment caps %v total %d exceed confirmed total %d", w.fees, sum, s.Request.RoutingFeeSat)
	}
}

func TestImplementationReviewPreflightRejectsPausedOffer(t *testing.T) {
	api := &fakeOrderAPI{robot: Robot{}, positions: map[string]*Position{"42": {ID: "42", Type: TypeBuy, Status: StatusPaused, PaymentMethod: "SEPA"}}}
	c := reviewCoordinator(api, &fakeWallet{}, &memoryStore{})
	if err := c.Check(context.Background(), reviewRequest()); err == nil {
		t.Fatal("paused order passed preflight")
	}
}

func TestImplementationReviewPreflightRejectsNumericActiveOrder(t *testing.T) {
	api := &fakeOrderAPI{robot: Robot{"active_order_id": float64(99)}, positions: map[string]*Position{"42": {ID: "42", Type: TypeBuy, Status: StatusPublic, PaymentMethod: "SEPA"}}}
	c := reviewCoordinator(api, &fakeWallet{}, &memoryStore{})
	if err := c.Check(context.Background(), reviewRequest()); err == nil {
		t.Fatal("robot active on order 99 passed preflight for order 42")
	}
}

func TestImplementationReviewRejectsUnknownStatus(t *testing.T) {
	if err := (&Position{ID: "42", Status: 999}).ValidateResponse(); err == nil {
		t.Fatal("unknown coordinator status accepted")
	}
}

type reviewExpiredAPI struct{ fakeOrderAPI }

func (*reviewExpiredAPI) GetPosition(context.Context, string) (*Position, error) {
	// The real Client.GetPosition returns ValidateResponse errors this way.
	p := &Position{ID: "42", Status: StatusExpired}
	return nil, p.ValidateResponse()
}

func TestImplementationReviewTerminalStatusParksSession(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	req := reviewRequest()
	_, err := store.CreateRoboSatsSession(ctx, &Session{Request: req, Phase: PhaseBondSubmitted, Payments: map[string]*Payment{"bond": {Invoice: "bond", Hash: "bond", Status: "IN_FLIGHT", Submitted: true}}})
	if err != nil {
		t.Fatal(err)
	}
	c := reviewCoordinator(&reviewExpiredAPI{}, &fakeWallet{}, store)
	reads := 0
	c.Sleep = func(context.Context, time.Duration) error {
		reads++
		if reads >= 3 {
			return context.Canceled
		}
		return nil
	}
	_, _ = c.Take(ctx, req)
	s, _ := store.LoadRoboSatsSession(ctx, req.RequestID)
	if s.Phase != PhaseNeedsReview {
		t.Fatalf("expired order retried %d times; durable phase remains %q instead of needs_review", reads, s.Phase)
	}
}

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestImplementationReviewFreshChatClientRecoversRegisteredKeys(t *testing.T) {
	const token = "review-chat-token"
	registered, err := NewIdentity(token)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := NewIdentity("review-peer-token")
	if err != nil {
		t.Fatal(err)
	}
	selfKey, err := unlockedEntity(registered.EncPrivKeyArmored, token)
	if err != nil {
		t.Fatal(err)
	}
	peerKey, err := unlockedEntity(peer.EncPrivKeyArmored, "review-peer-token")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := encryptChatMessage("payment details", peerKey, selfKey)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{BaseURL: "http://review.test", AuthToken: token})
	client.http = &http.Client{Transport: reviewTransport(func(r *http.Request) (*http.Response, error) {
		var body any
		switch r.URL.Path {
		case "/api/robot/":
			body = map[string]any{"public_key": registered.PublicKeyArmored, "encrypted_private_key": registered.EncPrivKeyArmored}
		case "/api/chat/":
			body = map[string]any{"peer_pubkey": peer.PublicKeyArmored, "messages": []map[string]any{{"index": 1, "nick": "peer", "message": cipher}}}
		default:
			return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
		}
		data, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	// PaymentResolver.clientFor returns a fresh client and immediately reads chat.
	thread, err := client.GetChatMessages(context.Background(), "42")
	if err != nil {
		t.Fatalf("real chat call sequence cannot decrypt registered-robot history: %v", err)
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Plaintext != "payment details" {
		t.Fatal("registered history not recovered")
	}
}
