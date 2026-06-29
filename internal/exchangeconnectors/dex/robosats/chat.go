package robosats

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// ChatMessage is one RoboSats trade-chat message after decryption.
type ChatMessage struct {
	// Index is the coordinator's monotonic message index (used as the send offset).
	Index int `json:"index"`
	// Time is the coordinator-stamped time string.
	Time string `json:"time"`
	// Nick is the sender's robot nick.
	Nick string `json:"nick"`
	// Plaintext is the decrypted message body, or "" if it could not be decrypted.
	Plaintext string `json:"plaintext"`
}

// ChatThread is the decrypted view of an order's trade chat.
type ChatThread struct {
	// OrderID is the RoboSats order id.
	OrderID string `json:"order_id"`
	// PeerConnected reports whether the counterparty is currently online.
	PeerConnected bool `json:"peer_connected"`
	// PeerPubKey is the counterparty's armored PGP public key (newlines normalized).
	PeerPubKey string `json:"peer_pubkey"`
	// Messages are the decrypted chat messages in coordinator order.
	Messages []ChatMessage `json:"messages"`
}

// chatResponse mirrors the RoboSats GET /api/chat/ payload.
//
// NOTE: the exact field names and armor escaping are coordinator-specific and must be
// verified against a live (Tor) coordinator; they are isolated here so only this struct and
// the path template (Config.ChatPath) need adjusting if they differ. The PGP crypto in
// chat_crypto.go is format-independent and unit-tested separately.
type chatResponse struct {
	PeerConnected bool   `json:"peer_connected"`
	PeerPubKey    string `json:"peer_pubkey"`
	Messages      []struct {
		Index   int    `json:"index"`
		Time    string `json:"time"`
		Nick    string `json:"nick"`
		Message string `json:"message"`
	} `json:"messages"`
}

// GetChatMessages fetches and decrypts the trade chat for an order. Each message is decrypted
// with the robot's own key; messages that fail to decrypt are returned with an empty Plaintext
// rather than failing the whole call (so an operator still sees the metadata).
func (c *Client) GetChatMessages(ctx context.Context, orderID string) (*ChatThread, error) {
	if orderID == "" {
		return nil, errors.New("robosats: order ID is required")
	}
	var raw chatResponse
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf(c.chatPath, orderID), nil, &raw); err != nil {
		return nil, err
	}

	self, err := c.unlockedSelf()
	if err != nil {
		return nil, err
	}
	var peer *openpgp.Entity
	if pk := unescapeArmor(raw.PeerPubKey); pk != "" {
		// A bad peer key only costs us signature verification; keep going so we can still
		// decrypt with our own key.
		peer, _ = readArmoredEntity(pk)
	}

	thread := &ChatThread{
		OrderID:       orderID,
		PeerConnected: raw.PeerConnected,
		PeerPubKey:    unescapeArmor(raw.PeerPubKey),
	}
	for _, m := range raw.Messages {
		plaintext, derr := decryptChatMessage(unescapeArmor(m.Message), self, peer)
		if derr != nil {
			plaintext = ""
		}
		thread.Messages = append(thread.Messages, ChatMessage{
			Index:     m.Index,
			Time:      m.Time,
			Nick:      m.Nick,
			Plaintext: plaintext,
		})
	}
	return thread, nil
}

// SendChatMessage encrypts+signs plaintext to the counterparty and posts it to the order's
// trade chat. It first reads the thread to obtain the peer's public key and the current
// message offset. Returns an error when the peer has not yet joined (no public key available).
func (c *Client) SendChatMessage(ctx context.Context, orderID, plaintext string) error {
	if orderID == "" {
		return errors.New("robosats: order ID is required")
	}
	thread, err := c.GetChatMessages(ctx, orderID)
	if err != nil {
		return err
	}
	if thread.PeerPubKey == "" {
		return errors.New("robosats: peer public key not available yet (counterparty not joined?)")
	}
	peer, err := readArmoredEntity(thread.PeerPubKey)
	if err != nil {
		return err
	}
	self, err := c.unlockedSelf()
	if err != nil {
		return err
	}
	armored, err := encryptChatMessage(plaintext, self, peer)
	if err != nil {
		return err
	}

	body := map[string]any{
		"PGP_message": escapeArmor(armored),
		"order_id":    orderID,
		"offset":      len(thread.Messages),
	}
	return c.request(ctx, http.MethodPost, fmt.Sprintf(c.chatPath, orderID), body, nil)
}

// unlockedSelf returns the robot's PGP entity with its private key decrypted by the token,
// ready to decrypt/sign chat messages.
func (c *Client) unlockedSelf() (*openpgp.Entity, error) {
	id, err := c.robotIdentity()
	if err != nil {
		return nil, err
	}
	return unlockedEntity(id.EncPrivKeyArmored, c.authToken)
}
