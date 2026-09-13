package mostro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"commons/telegram"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"golang.org/x/crypto/hkdf"
)

type chatKeys struct{ conversation, signing, address, author string }

func deriveChatKeys(secret, peer string) (chatKeys, error) {
	raw, err := hex.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		return chatKeys{}, fmt.Errorf("invalid trade secret")
	}
	peerBytes, err := hex.DecodeString(peer)
	if err != nil {
		return chatKeys{}, err
	}
	pub, err := schnorr.ParsePubKey(peerBytes)
	if err != nil {
		return chatKeys{}, err
	}
	priv, _ := btcec.PrivKeyFromBytes(raw)
	shared := btcec.GenerateSharedSecret(priv, pub)
	derive := func(domain string) (string, error) {
		for counter := 0; counter < 256; counter++ {
			info := []byte(domain)
			if counter > 0 {
				info = append(info, byte(counter))
			}
			b := make([]byte, 32)
			if _, err := io.ReadFull(hkdf.New(sha256.New, shared, nil, info), b); err != nil {
				return "", err
			}
			var scalar btcec.ModNScalar
			if !scalar.SetByteSlice(b) && !scalar.IsZero() {
				return hex.EncodeToString(b), nil
			}
		}
		return "", fmt.Errorf("chat key derivation exhausted")
	}
	conv, err := derive("mostro:chat:conv:v1")
	if err != nil {
		return chatKeys{}, err
	}
	sign, err := derive("mostro:chat:sign:v1")
	if err != nil {
		return chatKeys{}, err
	}
	addr, err := nostr.GetPublicKey(conv)
	if err != nil {
		return chatKeys{}, err
	}
	author, err := nostr.GetPublicKey(sign)
	return chatKeys{conv, sign, addr, author}, err
}
func buildChatEvent(text, trade string, k chatKeys, now time.Time) (nostr.Event, error) {
	if len(text) == 0 || len(text) > 16000 {
		return nostr.Event{}, fmt.Errorf("mostro: chat message must be 1–16000 bytes")
	}
	inner := nostr.Event{Kind: 1, CreatedAt: nostr.Timestamp(now.Unix()), Tags: nostr.Tags{}, Content: text}
	if err := inner.Sign(trade); err != nil {
		return nostr.Event{}, err
	}
	raw, err := json.Marshal(inner)
	if err != nil {
		return nostr.Event{}, err
	}
	key, err := nip44.GenerateConversationKey(k.address, k.conversation)
	if err != nil {
		return nostr.Event{}, err
	}
	cipher, err := nip44.Encrypt(string(raw), key)
	if err != nil {
		return nostr.Event{}, err
	}
	outer := nostr.Event{Kind: 14, CreatedAt: inner.CreatedAt, Tags: nostr.Tags{{"p", k.address}}, Content: cipher}
	err = outer.Sign(k.signing)
	return outer, err
}
func decodeChatEvent(e nostr.Event, k chatKeys, own, peer string, now time.Time) (nostr.Event, error) {
	if e.Kind != 14 || e.PubKey != k.author || !oneRecipient(e.Tags, k.address) || e.CreatedAt > nostr.Timestamp(now.Add(time.Minute).Unix()) || len(e.Content) > 65536 || !validEvent(e) {
		return nostr.Event{}, fmt.Errorf("mostro: invalid chat envelope")
	}
	key, err := nip44.GenerateConversationKey(k.address, k.conversation)
	if err != nil {
		return nostr.Event{}, err
	}
	raw, err := nip44.Decrypt(e.Content, key)
	if err != nil {
		return nostr.Event{}, err
	}
	var inner nostr.Event
	if err = json.Unmarshal([]byte(raw), &inner); err != nil {
		return inner, err
	}
	delta := int64(inner.CreatedAt - e.CreatedAt)
	if inner.Kind != 1 || (inner.PubKey != own && inner.PubKey != peer) || delta > 60 || delta < -60 || len(inner.Content) > 16000 || !validEvent(inner) {
		return nostr.Event{}, fmt.Errorf("mostro: invalid signed chat sender or timestamp")
	}
	return inner, nil
}
func oneRecipient(tags nostr.Tags, want string) bool {
	count := 0
	for _, t := range tags {
		if len(t) > 0 && t[0] == "p" {
			count++
			if len(t) != 2 || t[1] != want {
				return false
			}
		}
	}
	return count == 1
}
func (m *Manager) chatSession(ctx context.Context, ref string) (*Session, func(), error) {
	if m == nil || m.Store == nil {
		return nil, nil, fmt.Errorf("mostro chat is not configured")
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("mostro: transaction-scoped chat reference required")
	}
	id, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := m.Store.LockMostro(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	s, err := m.Store.LoadMostroSession(ctx, id)
	if err != nil || s == nil || s.TradeRef() != ref {
		unlock()
		return nil, nil, fmt.Errorf("mostro: chat session not found")
	}
	if err = m.poll(ctx, s); err != nil {
		unlock()
		return nil, nil, err
	}
	if s.PeerPubkey == "" {
		unlock()
		return nil, nil, fmt.Errorf("mostro: authenticated peer key not yet received")
	}
	return s, unlock, nil
}

// poll replays authenticated daemon events without initiating payments. This
// also discovers delayed peer disclosures after Take has returned.
func (m *Manager) poll(ctx context.Context, s *Session) error {
	_, trade, err := s.keys()
	if err != nil {
		return err
	}
	since := nostr.Timestamp(s.CreatedAt.Add(-48 * time.Hour).Unix())
	kind := 14
	if s.Request.Protocol == 1 {
		kind = 1059
	}
	f := nostr.Filter{Kinds: []int{kind}, Tags: nostr.TagMap{"p": []string{s.TradePubkey}}, Since: &since, Limit: 2000}
	if kind == 14 {
		f.Authors = []string{s.Request.Node}
	}
	events, err := m.transport().Query(ctx, f)
	if err != nil {
		return err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt < events[j].CreatedAt })
	for _, e := range events {
		if s.Seen[e.ID] || e.CreatedAt > nostr.Now()+60 {
			continue
		}
		res, err := DecodeResponse(e, s.Request.Node, trade, s.Request.Protocol)
		if err != nil || res.Message.ID != s.Request.OrderID {
			continue
		}
		if res.Message.RequestID != nil && *res.Message.RequestID != s.Request.RequestID {
			continue
		}
		if err = m.apply(ctx, s, res.Message); err != nil {
			s.State = "needs_review"
			s.LastError = err.Error()
		}
		s.Seen[e.ID] = true
		if err = m.Store.SaveMostroSession(ctx, s, &e); err != nil {
			return err
		}
	}
	return nil
}

// SendMessage is idempotent for the same text within a trade, so an ambiguous
// publication response cannot duplicate automated payment-detail requests.
func (m *Manager) SendMessage(ctx context.Context, ref, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	s, unlock, err := m.chatSession(ctx, ref)
	if err != nil {
		return err
	}
	defer unlock()
	_, trade, err := s.keys()
	if err != nil {
		return err
	}
	k, err := deriveChatKeys(trade, s.PeerPubkey)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(text))
	event, err := m.Store.MostroOutbox(ctx, s.ID, "chat:"+hex.EncodeToString(digest[:]), func() (nostr.Event, error) { return buildChatEvent(text, trade, k, time.Now()) })
	if err != nil {
		return err
	}
	since := nostr.Now()
	c, err := m.transport().Open(ctx, nostr.Filter{Kinds: []int{14}, Authors: []string{k.author}, Tags: nostr.TagMap{"p": []string{k.address}}, Since: &since, Limit: 1})
	if err != nil {
		return err
	}
	defer c.Close()
	if err = c.Publish(ctx, event); err != nil {
		return err
	}
	if err = m.Store.AckMostroOutbox(ctx, event.ID); err != nil {
		return err
	}
	inner, err := decodeChatEvent(event, k, s.TradePubkey, s.PeerPubkey, time.Now())
	if err != nil {
		return err
	}
	return m.Store.SaveMostroChatEvent(ctx, s.ID, event, inner.ID)
}
func (m *Manager) Messages(ctx context.Context, ref string) ([]telegram.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	s, unlock, err := m.chatSession(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer unlock()
	_, trade, err := s.keys()
	if err != nil {
		return nil, err
	}
	k, err := deriveChatKeys(trade, s.PeerPubkey)
	if err != nil {
		return nil, err
	}
	stored, err := m.Store.LoadMostroChatEvents(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	since := nostr.Timestamp(s.CreatedAt.Unix())
	now := time.Now()
	for _, e := range stored {
		if e.CreatedAt > since && e.CreatedAt <= nostr.Timestamp(now.Unix()) {
			since = e.CreatedAt
		}
	}
	since -= 60
	events, err := m.transport().Query(ctx, nostr.Filter{Kinds: []int{14}, Authors: []string{k.author}, Tags: nostr.TagMap{"p": []string{k.address}}, Since: &since, Limit: 1000})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var inners []nostr.Event
	for _, e := range append(stored, events...) {
		inner, err := decodeChatEvent(e, k, s.TradePubkey, s.PeerPubkey, now)
		if err != nil || seen[inner.ID] {
			continue
		}
		seen[inner.ID] = true
		if err = m.Store.SaveMostroChatEvent(ctx, s.ID, e, inner.ID); err != nil {
			return nil, err
		}
		inners = append(inners, inner)
	}
	sort.Slice(inners, func(i, j int) bool { return inners[i].CreatedAt < inners[j].CreatedAt })
	out := make([]telegram.Message, 0, len(inners))
	for _, e := range inners {
		out = append(out, telegram.Message{Time: time.Unix(int64(e.CreatedAt), 0).UTC().Format(time.RFC3339), Sender: e.PubKey, Outbound: e.PubKey == s.TradePubkey, Text: e.Content})
	}
	return out, nil
}
