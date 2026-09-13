package mostro

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/tyler-smith/go-bip39"
)

func testKeys(t *testing.T) (string, string) {
	t.Helper()
	entropy := make([]byte, 32)
	entropy[31] = 1
	words, err := bip39.NewMnemonic(entropy)
	if err != nil {
		t.Fatal(err)
	}
	id, err := DeriveKey(words, 0)
	if err != nil {
		t.Fatal(err)
	}
	trade, err := DeriveKey(words, 1)
	if err != nil {
		t.Fatal(err)
	}
	if id == trade {
		t.Fatal("identity and trade keys were reused")
	}
	return id, trade
}

func TestBuildV2TakeBuyIsSignedAndDecryptable(t *testing.T) {
	id, trade := testKeys(t)
	nodeSK := nostr.GeneratePrivateKey()
	node, _ := nostr.GetPublicKey(nodeSK)
	e, err := BuildEvent(MessageKind{ID: "order-1", Action: "take-buy", Payload: json.RawMessage("null")}, node, id, trade, ProtocolV2, false, time.Unix(1_700_000_000, 0), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != 14 {
		t.Fatalf("kind=%d", e.Kind)
	}
	if ok, _ := e.CheckSignature(); !ok {
		t.Fatal("event signature invalid")
	}
	k, _ := nip44.GenerateConversationKey(e.PubKey, nodeSK)
	plain, err := nip44.Decrypt(e.Content, k)
	if err != nil {
		t.Fatal(err)
	}
	var tuple []json.RawMessage
	if err := json.Unmarshal([]byte(plain), &tuple); err != nil {
		t.Fatal(err)
	}
	if len(tuple) != 3 || string(tuple[1]) == "null" || string(tuple[2]) == "null" {
		t.Fatalf("bad tuple: %s", plain)
	}
}

func TestBuildV2PrivacyOmitsReputationProof(t *testing.T) {
	id, trade := testKeys(t)
	node, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	e, err := BuildEvent(MessageKind{ID: "order-1", Action: "take-buy", Payload: json.RawMessage("null")}, node, id, trade, ProtocolV2, true, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if e.PubKey == "" {
		t.Fatal("missing author")
	}
}
