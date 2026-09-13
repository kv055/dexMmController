package mostro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"strings"
	"testing"
)

const reviewMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func TestProtocolRegressionMostroDerivationPath(t *testing.T) {
	for _, index := range []uint32{0, 1, 2} {
		key, err := bip32.NewMasterKey(bip39.NewSeed(reviewMnemonic, ""))
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range []uint32{bip32.FirstHardenedChild + 44, bip32.FirstHardenedChild + 1237, bip32.FirstHardenedChild + 38383, 0, index} {
			key, err = key.NewChildKey(child)
			if err != nil {
				t.Fatal(err)
			}
		}
		actual, err := DeriveKey(reviewMnemonic, index)
		if err != nil {
			t.Fatal(err)
		}
		if actual != hex.EncodeToString(key.Key) {
			t.Errorf("index %d does not match documented m/44'/1237'/38383'/0/index", index)
		}
	}
}

func TestProtocolRegressionSignaturesMatchDaemonSerialization(t *testing.T) {
	identity, _ := DeriveKey(reviewMnemonic, 0)
	trade, _ := DeriveKey(reviewMnemonic, 1)
	requestID := uint64(42)
	index := int64(1)
	// Field order is from mostro-core MessageKind's serde Serialize implementation.
	type daemonKind struct {
		Version    int             `json:"version"`
		RequestID  *uint64         `json:"request_id"`
		TradeIndex *int64          `json:"trade_index"`
		ID         string          `json:"id,omitempty"`
		Action     string          `json:"action"`
		Payload    json.RawMessage `json:"payload"`
	}
	for _, version := range []int{1, 2} {
		msg := OrderMessage{Order: MessageKind{Version: version, RequestID: &requestID, ID: "308e1272-d5f4-47e6-bd97-3504baea9c23", TradeIndex: &index, Action: "take-buy", Payload: json.RawMessage("null")}}
		env, wire, err := signedEnvelope(msg, identity, trade, false)
		if err != nil {
			t.Fatal(err)
		}
		var daemon struct {
			Order daemonKind `json:"order"`
		}
		if err = json.Unmarshal(wire, &daemon); err != nil {
			t.Fatal(err)
		}
		reserialized, err := json.Marshal(daemon)
		if err != nil {
			t.Fatal(err)
		}
		signatureBytes, _ := hex.DecodeString(*env.TradeSig)
		signature, _ := schnorr.ParseSignature(signatureBytes)
		pub, _ := nostr.GetPublicKey(trade)
		pubBytes, _ := hex.DecodeString(pub)
		pk, _ := schnorr.ParsePubKey(pubBytes)
		digest := sha256.Sum256(reserialized)
		if !signature.Verify(digest[:], pk) {
			t.Errorf("v%d trade signature fails verification after daemon reserialization", version)
		}
		if version == 2 {
			proofBytes, _ := hex.DecodeString(env.IdentityProof[1])
			proof, _ := schnorr.ParseSignature(proofBytes)
			identityPub, _ := hex.DecodeString(env.IdentityProof[0])
			identityPK, _ := schnorr.ParsePubKey(identityPub)
			hash := sha256.Sum256([]byte("mostro-transport-v2-identity:" + pub + ":" + string(reserialized)))
			if !proof.Verify(hash[:], identityPK) {
				t.Error("v2 identity proof fails daemon verification")
			}
		}
	}
}

func TestProtocolRegressionRejectWrongKindResponse(t *testing.T) {
	_, trade := reviewKeys(t)
	nodeSK := nostr.GeneratePrivateKey()
	node, _ := nostr.GetPublicKey(nodeSK)
	tradePub, _ := nostr.GetPublicKey(trade)
	key, _ := nip44.GenerateConversationKey(tradePub, nodeSK)
	payload := `[{"order":{"version":2,"id":"308e1272-d5f4-47e6-bd97-3504baea9c23","action":"pay-invoice","payload":null}},null,null]`
	ciphertext, err := nip44.Encrypt(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", tradePub}}, Content: ciphertext}
	if err = event.Sign(nodeSK); err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeResponse(event, node, trade, 2); err == nil {
		t.Error("accepted signed kind-1 event as protocol-v2 response")
	}
	event.Kind = 14
	if err = event.Sign(nodeSK); err != nil {
		t.Fatal(err)
	}
	event.ID = strings.Repeat("0", 64)
	if _, err = DecodeResponse(event, node, trade, 2); err == nil {
		t.Error("accepted response with incorrect event id")
	}
}

func reviewKeys(t *testing.T) (string, string) {
	t.Helper()
	a, e := DeriveKey(reviewMnemonic, 0)
	if e != nil {
		t.Fatal(e)
	}
	b, e := DeriveKey(reviewMnemonic, 1)
	if e != nil {
		t.Fatal(e)
	}
	return a, b
}
