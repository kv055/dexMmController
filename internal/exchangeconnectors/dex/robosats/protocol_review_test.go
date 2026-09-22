package robosats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewTakeUsesCoordinatorContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/order/" || r.URL.Query().Get("order_id") != "42" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["action"] != "take" {
			http.Error(w, "take action required", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"id":"42","type":0,"status":3}`))
	}))
	defer srv.Close()
	_, err := NewClient(Config{BaseURL: srv.URL}).TakeOffer(context.Background(), "42")
	if err != nil {
		t.Fatalf("take against coordinator contract: %v", err)
	}
}

func TestReviewPositionAcceptsNumericFields(t *testing.T) {
	var pos Position
	err := json.Unmarshal([]byte(`{"id":42,"type":0,"status":3,"amount":"100.00","bond_invoice":"lnbc1bond"}`), &pos)
	if err != nil {
		t.Fatalf("decode coordinator order: %v", err)
	}
	if pos.ID != "42" || pos.Type != TypeBuy || pos.Status != 3 || pos.BondInvoice != "lnbc1bond" {
		t.Fatalf("decoded %+v", pos)
	}
}

func TestReviewPositionRetainsTopLevelBond(t *testing.T) {
	var pos Position
	if err := json.Unmarshal([]byte(`{"type":0,"status":7,"bond_invoice":"lnbc1bond","escrow_invoice":"lnbc1escrow"}`), &pos); err != nil {
		t.Fatal(err)
	}
	if pos.BondInvoice != "lnbc1bond" || pos.EscrowInvoice != "lnbc1escrow" {
		t.Fatalf("typed invoices lost: %+v", pos)
	}
	if len(pos.Raw) == 0 {
		t.Fatal("top-level funding invoices discarded; deposit extractor only examines Position.Raw")
	}
}

func TestReviewRecoveredRobotCanDecryptChat(t *testing.T) {
	registered, err := NewIdentity("review-fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := NewIdentity("review-peer-token")
	if err != nil {
		t.Fatal(err)
	}
	registeredKey, err := unlockedEntity(registered.EncPrivKeyArmored, "review-fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	peerKey, err := unlockedEntity(peer.EncPrivKeyArmored, "review-peer-token")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptChatMessage("payment details", peerKey, registeredKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/robot/":
			_ = json.NewEncoder(w).Encode(map[string]any{"public_key": registered.PublicKeyArmored, "encrypted_private_key": registered.EncPrivKeyArmored})
		case "/api/chat/":
			_ = json.NewEncoder(w).Encode(map[string]any{"peer_pubkey": peer.PublicKeyArmored, "messages": []map[string]any{{"index": 1, "nick": "peer", "message": ciphertext}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := NewClient(Config{BaseURL: srv.URL}).WithAuthToken("review-fixture-token")
	if _, err := client.GetRobot(context.Background()); err != nil {
		t.Fatal(err)
	}
	thread, err := client.GetChatMessages(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Plaintext != "payment details" {
		t.Fatal("client failed to decrypt message encrypted to its registered robot key after fetching robot")
	}
}
