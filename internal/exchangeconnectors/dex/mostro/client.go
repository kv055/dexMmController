// Package mostro implements the authenticated Nostr transport used to take
// Mostro orders, with durable orchestration in Manager.
package mostro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/nbd-wtf/go-nostr/nip59"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
)

const (
	ProtocolV1 = 1
	ProtocolV2 = 2
)

type OrderMessage struct {
	Order MessageKind `json:"order"`
}

type MessageKind struct {
	Version    int             `json:"version"`
	RequestID  *uint64         `json:"request_id"`
	TradeIndex *int64          `json:"trade_index"`
	ID         string          `json:"id,omitempty"`
	Action     string          `json:"action"`
	Payload    json.RawMessage `json:"payload"`
}

type Envelope struct {
	Message       OrderMessage
	TradeSig      *string
	IdentityProof *[2]string
}

type Response struct {
	Message MessageKind
	Raw     json.RawMessage
}

func DeriveKey(mnemonic string, index uint32) (string, error) {
	if !bip39.IsMnemonicValid(strings.TrimSpace(mnemonic)) {
		return "", fmt.Errorf("mostro: invalid BIP-39 mnemonic")
	}
	key, err := bip32.NewMasterKey(bip39.NewSeed(strings.TrimSpace(mnemonic), ""))
	if err != nil {
		return "", err
	}
	if index >= bip32.FirstHardenedChild {
		return "", fmt.Errorf("mostro: trade index exhausted")
	}
	path := []uint32{bip32.FirstHardenedChild + 44, bip32.FirstHardenedChild + 1237, bip32.FirstHardenedChild + 38383, 0, index}
	for _, child := range path {
		key, err = key.NewChildKey(child)
		if err != nil {
			return "", fmt.Errorf("mostro: derive key %d: %w", index, err)
		}
	}
	return hex.EncodeToString(key.Key), nil
}

func signedEnvelope(msg OrderMessage, identitySK, tradeSK string, privacy bool) (Envelope, []byte, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return Envelope{}, nil, err
	}
	env := Envelope{Message: msg}
	if privacy {
		return env, raw, nil
	}
	tradeSig, err := signDigest(tradeSK, raw)
	if err != nil {
		return Envelope{}, nil, err
	}
	tradePub, err := nostr.GetPublicKey(tradeSK)
	if err != nil {
		return Envelope{}, nil, err
	}
	identityPub, err := nostr.GetPublicKey(identitySK)
	if err != nil {
		return Envelope{}, nil, err
	}
	proofPayload := []byte("mostro-transport-v2-identity:" + tradePub + ":" + string(raw))
	identitySig, err := signDigest(identitySK, proofPayload)
	if err != nil {
		return Envelope{}, nil, err
	}
	env.TradeSig = &tradeSig
	env.IdentityProof = &[2]string{identityPub, identitySig}
	return env, raw, nil
}

func signDigest(secret string, message []byte) (string, error) {
	b, err := hex.DecodeString(secret)
	if err != nil || len(b) != 32 {
		return "", fmt.Errorf("mostro: invalid secret key")
	}
	priv, _ := btcec.PrivKeyFromBytes(b)
	h := sha256.Sum256(message)
	sig, err := schnorr.Sign(priv, h[:])
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig.Serialize()), nil
}

func marshalTuple(env Envelope, version int) ([]byte, error) {
	if version == ProtocolV1 {
		return json.Marshal([]any{env.Message, env.TradeSig})
	}
	return json.Marshal([]any{env.Message, env.TradeSig, env.IdentityProof})
}

func BuildEvent(msg MessageKind, nodePubKey, identitySK, tradeSK string, protocol int, privacy bool, now time.Time, ttl time.Duration) (nostr.Event, error) {
	if protocol != ProtocolV1 && protocol != ProtocolV2 {
		return nostr.Event{}, fmt.Errorf("mostro: unsupported protocol version %d", protocol)
	}
	msg.Version = protocol
	if privacy {
		msg.TradeIndex = nil
	}
	wrapped := OrderMessage{Order: msg}
	env, _, err := signedEnvelope(wrapped, identitySK, tradeSK, privacy)
	if err != nil {
		return nostr.Event{}, err
	}
	tuple, err := marshalTuple(env, protocol)
	if err != nil {
		return nostr.Event{}, err
	}
	tradePub, err := nostr.GetPublicKey(tradeSK)
	if err != nil {
		return nostr.Event{}, err
	}
	if protocol == ProtocolV1 {
		rumor := nostr.Event{PubKey: tradePub, CreatedAt: nostr.Timestamp(now.Unix()), Kind: nostr.KindTextNote, Tags: nostr.Tags{}, Content: string(tuple)}
		rumor.ID = rumor.GetID()
		sealSK := identitySK
		if privacy {
			sealSK = tradeSK
		}
		return nip59.GiftWrap(rumor, nodePubKey,
			func(s string) (string, error) {
				k, e := nip44.GenerateConversationKey(nodePubKey, sealSK)
				if e != nil {
					return "", e
				}
				return nip44.Encrypt(s, k)
			},
			func(e *nostr.Event) error { return e.Sign(sealSK) }, func(e *nostr.Event) {
				e.Tags = append(e.Tags, nostr.Tag{"expiration", strconv.FormatInt(now.Add(ttl).Unix(), 10)})
			})
	}
	key, err := nip44.GenerateConversationKey(nodePubKey, tradeSK)
	if err != nil {
		return nostr.Event{}, err
	}
	ciphertext, err := nip44.Encrypt(string(tuple), key)
	if err != nil {
		return nostr.Event{}, err
	}
	event := nostr.Event{CreatedAt: nostr.Timestamp(now.Unix()), Kind: 14, Tags: nostr.Tags{{"p", nodePubKey}, {"expiration", strconv.FormatInt(now.Add(ttl).Unix(), 10)}}, Content: ciphertext}
	if err := event.Sign(tradeSK); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func DecodeResponse(event nostr.Event, nodePubKey, tradeSK string, protocol int) (Response, error) {
	if protocol != ProtocolV1 && protocol != ProtocolV2 {
		return Response{}, fmt.Errorf("mostro: unsupported protocol")
	}
	wantKind := 14
	if protocol == ProtocolV1 {
		wantKind = 1059
	}
	pub, err := nostr.GetPublicKey(tradeSK)
	if err != nil {
		return Response{}, err
	}
	if len(event.Content) > 65536 || event.Kind != wantKind || !event.CheckID() || !oneRecipient(event.Tags, pub) {
		return Response{}, fmt.Errorf("mostro: invalid event envelope")
	}
	if event.PubKey != nodePubKey && protocol == ProtocolV2 {
		return Response{}, fmt.Errorf("mostro: response author does not match selected node")
	}
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		return Response{}, fmt.Errorf("mostro: invalid response signature")
	}
	var plaintext string
	if protocol == ProtocolV1 {
		decrypt := func(pubkey, ciphertext string) (string, error) {
			k, e := nip44.GenerateConversationKey(pubkey, tradeSK)
			if e != nil {
				return "", e
			}
			return nip44.Decrypt(ciphertext, k)
		}
		sealJSON, err := decrypt(event.PubKey, event.Content)
		if err != nil {
			return Response{}, err
		}
		var seal nostr.Event
		if json.Unmarshal([]byte(sealJSON), &seal) != nil || seal.Kind != 13 || seal.PubKey != nodePubKey || !validEvent(seal) {
			return Response{}, fmt.Errorf("mostro: invalid authenticated seal")
		}
		rumorJSON, err := decrypt(seal.PubKey, seal.Content)
		if err != nil {
			return Response{}, err
		}
		var rumor nostr.Event
		if json.Unmarshal([]byte(rumorJSON), &rumor) != nil || rumor.Kind != 1 || rumor.PubKey != nodePubKey || rumor.Sig != "" || !rumor.CheckID() {
			return Response{}, fmt.Errorf("mostro: invalid rumor")
		}

		plaintext = rumor.Content
	} else {
		key, err := nip44.GenerateConversationKey(nodePubKey, tradeSK)
		if err != nil {
			return Response{}, err
		}
		plaintext, err = nip44.Decrypt(event.Content, key)
		if err != nil {
			return Response{}, err
		}
	}
	var tuple []json.RawMessage
	if err := json.Unmarshal([]byte(plaintext), &tuple); err != nil || len(tuple) < 2 {
		return Response{}, fmt.Errorf("mostro: malformed response tuple")
	}
	wantLen := 3
	if protocol == ProtocolV1 {
		wantLen = 2
	}
	if len(tuple) != wantLen {
		return Response{}, fmt.Errorf("mostro: wrong tuple length")
	}
	var wrapper map[string]MessageKind
	if err := json.Unmarshal(tuple[0], &wrapper); err != nil || len(wrapper) != 1 {
		return Response{}, fmt.Errorf("mostro: malformed message")
	}
	for channel, msg := range wrapper {
		if channel != "order" && channel != "cant-do" && channel != "dispute" && channel != "rate" && channel != "restore" {
			return Response{}, fmt.Errorf("mostro: unsupported message channel")
		}
		if msg.Version != protocol || msg.Action == "" {
			return Response{}, fmt.Errorf("mostro: invalid protocol message")
		}
		return Response{Message: msg, Raw: append(json.RawMessage(nil), tuple[0]...)}, nil
	}
	return Response{}, fmt.Errorf("mostro: empty message")
}
