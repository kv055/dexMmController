package robosats

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type lostResponseWallet struct {
	*fakeWallet
	sends int
}

func (w *lostResponseWallet) HoldPayment(context.Context, string, string, int64) (string, error) {
	w.sends++
	return "", context.DeadlineExceeded
}

func TestInterruptedHoldObserverRecoversExpiredInvoiceByHash(t *testing.T) {
	store := &memoryStore{}
	wallet := &lostResponseWallet{fakeWallet: &fakeWallet{}}
	c := &Coordinator{Store: store, Wallet: wallet}
	s, _ := store.CreateRoboSatsSession(context.Background(), &Session{Request: TakeRequest{RequestID: 1, RoutingFeeSat: 10}, Payments: map[string]*Payment{"bond": {Purpose: "bond", Invoice: "invoice", Hash: "hash", Status: "requested", ExpiresAt: time.Now().Add(time.Hour)}}})
	if err := c.submitPending(context.Background(), s); err != context.DeadlineExceeded {
		t.Fatalf("lost observer: %v", err)
	}
	s, _ = store.LoadRoboSatsSession(context.Background(), 1)
	if !s.Payments["bond"].Attempted {
		t.Fatal("send intent was not durable")
	}
	s.Payments["bond"].ExpiresAt = time.Now().Add(-time.Minute)
	if err := c.submitPending(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if wallet.sends != 1 || !s.Payments["bond"].Submitted {
		t.Fatal("accepted HTLC was resent or not reconciled")
	}
}

type identityAPI struct {
	*fakeOrderAPI
	identity *Identity
}

func (a identityAPI) Identity() *Identity { return a.identity }

func TestAdmissionIdentitySurvivesCredentialAndRegistryRotation(t *testing.T) {
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	id, err := NewIdentity("original-robot")
	if err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Registry: map[string]string{"lake": "http://original.onion"}, Token: func(context.Context, int64) (string, error) { return "original-robot", nil }, Dial: func(string, string) (OrderAPI, error) { return identityAPI{&fakeOrderAPI{}, id}, nil }}
	s := &Session{Request: TakeRequest{CredentialID: 7, CoordinatorID: "lake", QuoteID: "q"}}
	if err := c.SnapshotIdentity(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.RecoveryEncrypted, "original-robot") || s.Request.TokenFingerprint == AuthHeaderToken("original-robot") {
		t.Fatal("recovery credential not protected")
	}
	c.Token = func(context.Context, int64) (string, error) { return "", fmt.Errorf("credential deactivated") }
	c.Dial = func(string, string) (OrderAPI, error) { return nil, fmt.Errorf("alias rotated") }
	c.Registry["lake"] = "http://new.onion"
	c.DialPinned = func(url, token string) (OrderAPI, error) {
		if url != "http://original.onion" || token != "original-robot" {
			t.Fatal("bound identity changed")
		}
		return NewClient(Config{BaseURL: url, AuthToken: token}), nil
	}
	api, err := c.SessionAPI(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if api.Identity().PublicKeyArmored != strings.TrimSpace(id.PublicKeyArmored) {
		t.Fatal("registered keys were regenerated")
	}
}

type lostChatAPI struct {
	*fakeOrderAPI
	sends int
}

func (a *lostChatAPI) SendChatMessage(_ context.Context, _ string, text string) error {
	a.sends++
	a.chats = &ChatThread{Messages: []ChatMessage{{Nick: "me", Plaintext: text, Own: true}}}
	return context.DeadlineExceeded
}
func TestChatLostAcknowledgementIsNotSentTwice(t *testing.T) {
	api := &lostChatAPI{fakeOrderAPI: &fakeOrderAPI{positions: map[string]*Position{"42": {ID: "42", IsTaker: true, IsSeller: true, Status: StatusSendingFiat, EscrowLocked: true}}}}
	store := &memoryStore{}
	_, _ = store.CreateRoboSatsSession(context.Background(), &Session{Request: TakeRequest{RequestID: 1, OrderID: "42"}, Payments: map[string]*Payment{}, Funded: true, RobotNick: "me"})
	c := &Coordinator{Store: store, Wallet: &fakeWallet{}, Token: func(context.Context, int64) (string, error) { return "robot", nil }, Dial: func(string, string) (OrderAPI, error) { return api, nil }}
	if err := c.ReconcileChat(context.Background(), 1, "original template", "me"); err == nil {
		t.Fatal("lost acknowledgement not reported")
	}
	if err := c.ReconcileChat(context.Background(), 1, "edited template", "me"); err != nil {
		t.Fatal(err)
	}
	s, _ := store.LoadRoboSatsSession(context.Background(), 1)
	if api.sends != 1 || !s.ChatSent || s.ChatTemplate != "original template" || s.ChatError != "" {
		t.Fatalf("chat recovery: %+v sends=%d", s, api.sends)
	}
}

func TestProtocolDoesNotDefaultMalformedStatusToZero(t *testing.T) {
	for _, raw := range []string{`{"type":0}`, `{"type":0,"status":"3garbage"}`, `{"type":null,"status":1}`, `{"type":0,"status":1.5}`} {
		var pos Position
		if err := json.Unmarshal([]byte(raw), &pos); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
