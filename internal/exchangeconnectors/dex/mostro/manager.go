package mostro

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"commons/credentialcrypto"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
)

type Manager struct {
	Mnemonic          string
	IdentityLabel     string
	EncryptedMnemonic []byte
	Relays            []string
	AllowedNodes      []string
	Store             Store
	Wallet            Wallet
	Transport         Transport
	Privacy           bool
	// Enabled requires an explicit operational opt-in. Protocol support alone
	// must not turn a previously read-only market into a trading venue.
	Enabled bool
}

func (m *Manager) transport() Transport {
	if m.Transport != nil {
		return m.Transport
	}
	return RelayTransport{Relays: m.Relays}
}
func (m *Manager) Ready() error {
	if m == nil || !m.Enabled {
		return fmt.Errorf("mostro: execution is disabled")
	}
	if m.Store == nil || m.Wallet == nil || len(m.AllowedNodes) == 0 {
		return fmt.Errorf("mostro: durable store, hold-payment wallet and node allowlist are required")
	}
	return nil
}

// TakeBuy with the old argument list cannot express a spend ceiling or quote
// provenance. Reject it before any key allocation or relay publication.
func (m *Manager) TakeBuy(context.Context, string, int, string, uint64) (*TakeResult, error) {
	return nil, fmt.Errorf("mostro: take requires signed quote provenance, selected amount and payment ceilings")
}

// Check performs read-only validation before the CEX leg can move money.
func (m *Manager) Check(ctx context.Context, req TakeRequest) error {
	if err := m.Ready(); err != nil {
		return err
	}
	if err := validateRequest(req); err != nil {
		return err
	}
	_, _, err := m.preflight(ctx, &req)
	return err
}
func (m *Manager) Take(ctx context.Context, req TakeRequest) (*TakeResult, error) {
	if err := m.Ready(); err != nil {
		return nil, err
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	unlock, err := m.Store.LockMostro(ctx, req.RequestID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := m.Store.LoadMostroSession(ctx, req.RequestID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		bond, protocol, err := m.preflight(ctx, &req)
		if err != nil {
			return nil, err
		}
		req.Protocol = protocol
		s, err = m.Store.CreateMostroSession(ctx, m.IdentityLabel, m.EncryptedMnemonic, func(cipher []byte, index int64) (*Session, error) {
			words, err := decryptMnemonic(cipher)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(m.Mnemonic) != "" && strings.TrimSpace(m.Mnemonic) != words {
				return nil, fmt.Errorf("mostro: configured mnemonic differs from stored identity; use a new label for a new identity")
			}
			identity, err := DeriveKey(words, 0)
			if err != nil {
				return nil, err
			}
			identityPub, err := nostr.GetPublicKey(identity)
			if err != nil {
				return nil, err
			}
			trade, err := DeriveKey(words, uint32(index))
			if err != nil {
				return nil, err
			}
			pub, err := nostr.GetPublicKey(trade)
			if err != nil {
				return nil, err
			}
			return &Session{IdentityPubkey: identityPub, Request: req, CreatedAt: time.Now().UTC(), TradePubkey: pub, Privacy: m.Privacy, State: "created", BondRequired: bond, Payments: map[string]*Payment{}, Seen: map[string]bool{}}, nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		if req.Protocol == 0 {
			req.Protocol = s.Request.Protocol
		}
		if req.LightningNode == "" {
			req.LightningNode = s.Request.LightningNode
		}
		if !reflect.DeepEqual(req, s.Request) {
			return nil, fmt.Errorf("mostro: retry changed the original trade terms")
		}
	}
	if s.State == "needs_review" {
		return nil, fmt.Errorf("mostro: trade requires review: %s", s.LastError)
	}

	err = m.run(ctx, s, "take-buy")
	if err != nil {
		return nil, err
	}
	return s.takeResult(), nil
}
func decryptMnemonic(cipher []byte) (string, error) {
	if !strings.HasPrefix(string(cipher), "enc:v1:") {
		return "", fmt.Errorf("mostro: stored mnemonic is not encrypted")
	}
	return credentialcrypto.Decrypt(string(cipher))
}
func (s *Session) keys() (string, string, error) {
	words, err := decryptMnemonic(s.EncryptedMnemonic)
	if err != nil {
		return "", "", err
	}
	if s.TradeIndex < 1 || s.TradeIndex >= 1<<31 {
		return "", "", fmt.Errorf("mostro: invalid persisted trade index")
	}
	id, err := DeriveKey(words, 0)
	if err != nil {
		return "", "", err
	}
	trade, err := DeriveKey(words, uint32(s.TradeIndex))
	if err != nil {
		return "", "", err
	}
	pub, err := nostr.GetPublicKey(trade)
	if err != nil || pub != s.TradePubkey {
		return "", "", fmt.Errorf("mostro: stored key does not match session")
	}
	return id, trade, nil
}
func validateRequest(r TakeRequest) error {
	if r.RequestID == 0 || r.RequestID > math.MaxInt64 {
		return fmt.Errorf("mostro: durable transaction id required")
	}
	if _, err := uuid.Parse(r.OrderID); err != nil {
		return fmt.Errorf("mostro: invalid order id")
	}
	for _, value := range []string{r.Node, r.SourceEventID} {
		b, err := hex.DecodeString(value)
		if err != nil || len(b) != 32 {
			return fmt.Errorf("mostro: signed node and source event are required")
		}
	}
	if r.Protocol != 0 && r.Protocol != 1 && r.Protocol != 2 {
		return fmt.Errorf("mostro: unsupported protocol")
	}
	if r.FiatAmount <= 0 || r.FiatCurrency == "" || r.PaymentMethod == "" || r.MaxEscrowSat <= 0 || r.MaxBondSat < 0 || r.RoutingFeeSat < 0 {
		return fmt.Errorf("mostro: selected fiat terms and payment ceilings are required")
	}
	return nil
}
func (m *Manager) preflight(ctx context.Context, r *TakeRequest) (bool, int, error) {
	allowed := false
	for _, node := range m.AllowedNodes {
		if strings.TrimSpace(node) == r.Node {
			allowed = true
		}
	}
	if !allowed {
		return false, 0, fmt.Errorf("mostro: selected node is not allowlisted")
	}
	info, err := m.transport().Query(ctx, nostr.Filter{Kinds: []int{38385}, Authors: []string{r.Node}, Tags: nostr.TagMap{"d": []string{r.Node}}, Limit: 100})
	if err != nil {
		return false, 0, err
	}
	node := latest(info, 38385, r.Node, r.Node)
	if node == nil || node.CreatedAt < nostr.Timestamp(time.Now().Add(-time.Hour).Unix()) {
		return false, 0, fmt.Errorf("mostro: fresh signed node capabilities unavailable")
	}
	protocol, err := strconv.Atoi(tag(*node, "protocol_version"))
	if err != nil || (protocol != 1 && protocol != 2) || (r.Protocol != 0 && r.Protocol != protocol) {
		return false, 0, fmt.Errorf("mostro: node protocol changed or is unsupported")
	}
	if tag(*node, "lnd_networks") != "mainnet" || tag(*node, "y") != "mostro" {
		return false, 0, fmt.Errorf("mostro: only mainnet Lightning nodes are supported")
	}
	for _, key := range []string{"pow", "pow_first_contact"} {
		v := tag(*node, key)
		if v != "" && v != "0" {
			return false, 0, fmt.Errorf("mostro: node requires unsupported proof of work")
		}
	}
	fee, err := strconv.ParseFloat(tag(*node, "fee"), 64)
	if err != nil || math.IsNaN(fee) || math.IsInf(fee, 0) || fee < 0 || fee > 0.05 {
		return false, 0, fmt.Errorf("mostro: invalid or excessive node fee")
	}
	lnpub := tag(*node, "lnd_node_pubkey")
	if len(lnpub) != 66 {
		return false, 0, fmt.Errorf("mostro: node has no signed Lightning destination")
	}
	if _, err := hex.DecodeString(lnpub); err != nil {
		return false, 0, fmt.Errorf("mostro: invalid Lightning destination")
	}
	r.LightningNode = lnpub
	policy := tag(*node, "bond_apply_to")
	if tag(*node, "bond_enabled") == "true" && policy != "make" && policy != "take" && policy != "both" {
		return false, 0, fmt.Errorf("mostro: unknown bond application policy")
	}
	bond := tag(*node, "bond_enabled") == "true" && policy != "make"
	if v := tag(*node, "bond_enabled"); v != "" && v != "true" && v != "false" {
		return false, 0, fmt.Errorf("mostro: unknown bond policy")
	}
	if bond && r.MaxBondSat <= 0 {
		return false, 0, fmt.Errorf("mostro: node requires a bond but no bond budget was approved")
	}
	events, err := m.transport().Query(ctx, nostr.Filter{Kinds: []int{38383}, Authors: []string{r.Node}, Tags: nostr.TagMap{"d": []string{r.OrderID}}, Limit: 100})
	if err != nil {
		return false, 0, err
	}
	order := latest(events, 38383, r.Node, r.OrderID)
	if order == nil || order.ID != r.SourceEventID {
		return false, 0, fmt.Errorf("mostro: selected signed quote is no longer current")
	}
	if tag(*order, "s") != "pending" || tag(*order, "k") != "buy" || tag(*order, "y") != "mostro" || tag(*order, "network") != "mainnet" || tag(*order, "layer") != "lightning" || tag(*order, "f") != r.FiatCurrency {
		return false, 0, fmt.Errorf("mostro: order is not a pending mainnet Lightning buy with the selected currency")
	}
	if exp := tag(*order, "expiration"); exp != "" {
		n, err := strconv.ParseInt(exp, 10, 64)
		if err != nil || n <= time.Now().Unix() {
			return false, 0, fmt.Errorf("mostro: quote expired")
		}
	}
	var min, max int64
	for _, t := range order.Tags {
		if len(t) >= 2 && t[0] == "fa" {
			min, err = strconv.ParseInt(t[1], 10, 64)
			max = min
			if err != nil {
				return false, 0, fmt.Errorf("mostro: invalid fiat range")
			}
			if len(t) == 3 {
				max, err = strconv.ParseInt(t[2], 10, 64)
				if err != nil {
					return false, 0, err
				}
			}
		}
	}
	if r.FiatAmount < min || r.FiatAmount > max || min <= 0 {
		return false, 0, fmt.Errorf("mostro: selected amount is outside the signed order range")
	}
	method := false
	for _, t := range order.Tags {
		if len(t) > 1 && t[0] == "pm" {
			for _, v := range t[1:] {
				if strings.EqualFold(strings.TrimSpace(v), r.PaymentMethod) {
					method = true
				}
			}
		}
	}
	if !method {
		return false, 0, fmt.Errorf("mostro: payment method does not match the signed quote")
	}
	if err := checkBudgets(*node, *order, *r, bond); err != nil {
		return false, 0, err
	}
	return bond, protocol, nil
}
func tag(e nostr.Event, key string) string {
	for _, t := range e.Tags {
		if len(t) > 1 && t[0] == key {
			return t[1]
		}
	}
	return ""
}
func latest(events []nostr.Event, kind int, author, d string) *nostr.Event {
	var out *nostr.Event
	for _, e := range events {
		if e.Kind != kind || e.PubKey != author || tag(e, "d") != d || e.CreatedAt > nostr.Now()+60 || !validEvent(e) {
			continue
		}
		if out == nil || e.CreatedAt > out.CreatedAt || (e.CreatedAt == out.CreatedAt && e.ID < out.ID) {
			v := e
			out = &v
		}
	}
	return out
}
func (m *Manager) run(ctx context.Context, s *Session, action string) error {
	identity, trade, err := s.keys()
	if err != nil {
		return err
	}
	since := nostr.Timestamp(s.CreatedAt.Add(-48 * time.Hour).Unix())
	kind := 14
	if s.Request.Protocol == 1 {
		kind = 1059
	}
	filter := nostr.Filter{Kinds: []int{kind}, Tags: nostr.TagMap{"p": []string{s.TradePubkey}}, Since: &since, Limit: 2000}
	if kind == 14 {
		filter.Authors = []string{s.Request.Node}
	}
	conn, err := m.transport().Open(ctx, filter)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := m.replay(ctx, s, trade, conn.History()); err != nil {
		return err
	}
	if err := m.drain(ctx, s, trade, conn); err != nil {
		return err
	}
	if s.Released {
		return m.confirmSettlement(ctx, s)
	}
	if action == "take-buy" && s.Funded {
		return m.confirmFunding(ctx, s)
	}
	event, err := m.Store.MostroOutbox(ctx, s.ID, action, func() (nostr.Event, error) {
		payload := json.RawMessage("null")
		if action == "take-buy" {
			payload = encodePayload(map[string]int64{"amount": s.Request.FiatAmount})
		}
		return BuildEvent(MessageKind{ID: s.Request.OrderID, RequestID: &s.Request.RequestID, TradeIndex: &s.TradeIndex, Action: action, Payload: payload}, s.Request.Node, identity, trade, s.Request.Protocol, s.Privacy, time.Now(), 15*time.Minute)
	})
	if err != nil {
		return err
	}
	// An expired immutable request can still have succeeded. Read its responses,
	// but never issue a fresh take to replace an ambiguous expired request.
	expiration, _ := strconv.ParseInt(tag(event, "expiration"), 10, 64)
	if expiration > time.Now().Unix() {
		if err := m.drain(ctx, s, trade, conn); err != nil {
			return err
		}
		if s.Released {
			return m.confirmSettlement(ctx, s)
		}
		if err = conn.Publish(ctx, event); err != nil {
			return err
		}
		if err = m.Store.AckMostroOutbox(ctx, event.ID); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := m.drain(ctx, s, trade, conn); err != nil {
			return err
		}
		if s.Released {
			return m.confirmSettlement(ctx, s)
		}
		if action == "take-buy" && s.Funded {
			return m.confirmFunding(ctx, s)
		}
		if action == "take-buy" {
			if err = m.payRequested(ctx, s, func() error { return m.drain(ctx, s, trade, conn) }); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("mostro: reconciliation timed out; retry transaction %d with the same session: %w", s.Request.RequestID, ctx.Err())
		case <-ticker.C:
			continue
		case e, ok := <-conn.Events():
			if !ok {
				return fmt.Errorf("mostro: response subscriptions closed")
			}
			if err := m.receive(ctx, s, trade, e); err != nil {
				return err
			}
		}
	}
}

// receive checkpoints authenticated responses before any next side effect.
func (m *Manager) receive(ctx context.Context, s *Session, trade string, e nostr.Event) error {
	if s.Seen[e.ID] || len(e.Content) > 65536 || e.CreatedAt > nostr.Now()+60 {
		return nil
	}
	res, err := DecodeResponse(e, s.Request.Node, trade, s.Request.Protocol)
	if err != nil {
		return nil
	}
	msg := res.Message
	if msg.ID != s.Request.OrderID && !(msg.ID == "" && msg.Action == "cant-do" && msg.RequestID != nil && *msg.RequestID == s.Request.RequestID) {
		return nil
	}
	if msg.RequestID != nil && *msg.RequestID != s.Request.RequestID {
		return nil
	}
	if err = m.apply(ctx, s, msg); err != nil && s.State != "needs_review" {
		s.State, s.LastError = "needs_review", err.Error()
	}
	s.Seen[e.ID] = true
	return m.Store.SaveMostroSession(ctx, s, &e)
}
func reviewError(s *Session) error {
	if s.State == "needs_review" {
		return fmt.Errorf("mostro: trade requires review: %s", s.LastError)
	}
	return nil
}
func (m *Manager) replay(ctx context.Context, s *Session, trade string, events []nostr.Event) error {
	// Relays send stored events newest first. Restore invoice prerequisites
	// before acknowledgements, including v1 envelopes with randomized times.
	rank := func(e nostr.Event) int {
		r, err := DecodeResponse(e, s.Request.Node, trade, s.Request.Protocol)
		if err != nil {
			return 3
		}
		switch r.Message.Action {
		case "pay-bond-invoice":
			return 0
		case "pay-invoice":
			return 1
		default:
			return 2
		}
	}
	ranks := make(map[string]int, len(events))
	for _, e := range events {
		ranks[e.ID] = rank(e)
	}
	sort.SliceStable(events, func(i, j int) bool {
		if a, b := ranks[events[i].ID], ranks[events[j].ID]; a != b {
			return a < b
		}
		return events[i].CreatedAt < events[j].CreatedAt
	})
	for _, e := range events {
		if err := m.receive(ctx, s, trade, e); err != nil {
			return err
		}
	}
	return reviewError(s)
}
func (m *Manager) drain(ctx context.Context, s *Session, trade string, conn Connection) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-conn.Events():
			if !ok {
				return fmt.Errorf("mostro: response subscriptions closed")
			}
			if err := m.receive(ctx, s, trade, e); err != nil {
				return err
			}
		default:
			return reviewError(s)
		}
	}
}
func (m *Manager) apply(ctx context.Context, s *Session, msg MessageKind) error {
	if err := reviewError(s); err != nil {
		return err
	}
	if s.Released || s.State == "released" {
		switch msg.Action {
		case "pay-invoice", "pay-bond-invoice", "hold-invoice-payment-accepted", "waiting-buyer-invoice", "buyer-invoice-accepted":
			return nil // delayed progress must never reopen a settled trade
		}
	}
	switch msg.Action {
	case "pay-invoice", "pay-bond-invoice":
		purpose := "escrow"
		if msg.Action == "pay-bond-invoice" {
			purpose = "bond"
			if !s.BondRequired {
				return fmt.Errorf("unexpected bond invoice")
			}
		}
		invoice, err := paymentRequest(msg.Payload)
		if err != nil {
			return err
		}
		if old := s.Payments[purpose]; old != nil {
			if old.Invoice != invoice {
				return fmt.Errorf("node changed the %s invoice; reconciliation required", purpose)
			}
			return nil
		}
		if purpose == "escrow" && s.BondRequired && s.Payments["bond"] == nil {
			return fmt.Errorf("escrow arrived before the required bond")
		}
		decoded, err := m.Wallet.DecodeInvoice(ctx, invoice)
		if err != nil {
			return err
		}
		amount, err := validateInvoice(invoice, decoded, s.Request, purpose)
		if err != nil {
			return err
		}
		for _, p := range s.Payments {
			if p.Hash == decoded.PaymentHash {
				return fmt.Errorf("bond and escrow reuse the same payment hash")
			}
		}
		s.Payments[purpose] = &Payment{Purpose: purpose, Invoice: invoice, Hash: decoded.PaymentHash, AmountSat: amount, Status: "requested"}
		s.State = "awaiting_payment"
	case "hold-invoice-payment-accepted", "waiting-buyer-invoice", "buyer-invoice-accepted":
		if s.Payments["escrow"] == nil {
			return fmt.Errorf("escrow accepted before its invoice was recorded")
		}
		s.Funded = true
		s.State = "funded"
	case "fiat-sent-ok":
		s.FiatSent = true
	case "released", "hold-invoice-payment-settled":
		s.Released = true
	case "trade-pubkey":
	case "cant-do", "canceled", "hold-invoice-payment-canceled", "bond-slashed", "dispute-initiated-by-peer", "dispute-initiated-by-you", "admin-canceled", "admin-settled", "cooperative-cancel-initiated-by-peer", "cooperative-cancel-accepted":
		return fmt.Errorf("node reported %s; operator reconciliation required", msg.Action)
	}
	var payload struct {
		Peer *struct {
			Pubkey string `json:"pubkey"`
		} `json:"peer"`
	}
	if json.Unmarshal(msg.Payload, &payload) == nil && payload.Peer != nil {
		key := payload.Peer.Pubkey
		b, err := hex.DecodeString(key)
		if err != nil || len(b) != 32 {
			return fmt.Errorf("invalid peer key")
		}
		if s.PeerPubkey != "" && s.PeerPubkey != key {
			return fmt.Errorf("node changed counterparty key")
		}
		s.PeerPubkey = key
	}
	return nil
}
func paymentRequest(raw json.RawMessage) (string, error) {
	var p struct {
		PaymentRequest []json.RawMessage `json:"payment_request"`
	}
	if json.Unmarshal(raw, &p) != nil || len(p.PaymentRequest) != 3 {
		return "", fmt.Errorf("mostro: malformed payment-request payload")
	}
	var invoice string
	if json.Unmarshal(p.PaymentRequest[1], &invoice) != nil || invoice == "" {
		return "", fmt.Errorf("mostro: missing payment invoice")
	}
	return invoice, nil
}
func (m *Manager) payRequested(ctx context.Context, s *Session, beforePayment func() error) error {
	for _, purpose := range []string{"bond", "escrow"} {
		// A bond attempt can take time. Reconcile messages that arrived during
		// it before initiating the separate escrow payment.
		if err := beforePayment(); err != nil {
			return err
		}
		if s.Released || s.Funded {
			return nil
		}
		p := s.Payments[purpose]
		if p == nil {
			continue
		}
		if p.Status == "IN_FLIGHT" {
			status, err := m.Wallet.TrackHoldPayment(ctx, p.Hash)
			if err != nil {
				return err
			}
			if status != "IN_FLIGHT" {
				return m.review(ctx, s, fmt.Errorf("%s hold payment changed to %s", purpose, status))
			}
			continue
		}
		if p.Status == "SUCCEEDED" {
			return m.review(ctx, s, fmt.Errorf("%s settled before release", purpose))
		}
		if p.Status == "FAILED" {
			return fmt.Errorf("mostro: %s payment failed; manual retry required", purpose)
		}
		// This intent (hash and exact invoice) is durable BEFORE LND sees it.
		if err := m.Store.SaveMostroSession(ctx, s, nil); err != nil {
			return err
		}
		feeLimit := s.Request.RoutingFeeSat
		if s.BondRequired {
			if purpose == "bond" {
				feeLimit /= 2
			} else {
				feeLimit -= feeLimit / 2
			}
		}
		status, err := m.Wallet.HoldPayment(ctx, p.Invoice, p.Hash, feeLimit)
		if err != nil {
			return err
		}
		p.Status = status
		if err = m.Store.SaveMostroSession(ctx, s, nil); err != nil {
			return err
		}
		if status != "IN_FLIGHT" {
			return m.review(ctx, s, fmt.Errorf("unexpected %s payment state %s", purpose, status))
		}
	}
	return nil
}
func (m *Manager) Release(ctx context.Context, requestID uint64, orderID string) error {
	if m == nil || m.Store == nil || m.Wallet == nil {
		return fmt.Errorf("mostro: recovery dependencies are not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	unlock, err := m.Store.LockMostro(ctx, requestID)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.Store.LoadMostroSession(ctx, requestID)
	if err != nil {
		return err
	}
	if s == nil || (s.Request.OrderID != orderID && s.TradeRef() != orderID) {
		return fmt.Errorf("mostro: release session does not match transaction")
	}
	if err := m.Store.AuthorizeMostroRelease(ctx, requestID); err != nil {
		return err
	}
	if !s.Funded || s.State == "needs_review" {
		return fmt.Errorf("mostro: session is not releasable")
	}
	return m.run(ctx, s, "release")
}
func (m *Manager) confirmSettlement(ctx context.Context, s *Session) error {
	if err := reviewError(s); err != nil {
		return err
	}
	if s.BondRequired && s.Payments["bond"] == nil {
		return fmt.Errorf("mostro: missing required bond payment")
	}
	escrow := s.Payments["escrow"]
	if escrow == nil {
		return fmt.Errorf("mostro: missing escrow payment")
	}
	status, err := m.Wallet.TrackHoldPayment(ctx, escrow.Hash)
	if err != nil {
		return err
	}
	escrow.Status = status
	if status != "SUCCEEDED" {
		return fmt.Errorf("mostro: release acknowledged but escrow payment is %s", status)
	}
	if bond := s.Payments["bond"]; bond != nil {
		status, err = m.Wallet.TrackHoldPayment(ctx, bond.Hash)
		if err != nil {
			return err
		}
		bond.Status = status
		if status != "FAILED" {
			return fmt.Errorf("mostro: bond not returned (payment %s)", status)
		}
	}
	s.State = "released"
	return m.Store.SaveMostroSession(ctx, s, nil)
}

func (m *Manager) confirmFunding(ctx context.Context, s *Session) error {
	for _, purpose := range []string{"bond", "escrow"} {
		p := s.Payments[purpose]
		if p == nil {
			if purpose == "escrow" || s.BondRequired {
				return fmt.Errorf("mostro: missing %s payment", purpose)
			}
			continue
		}
		status, err := m.Wallet.TrackHoldPayment(ctx, p.Hash)
		if err != nil {
			return err
		}
		p.Status = status
		if status != "IN_FLIGHT" {
			return fmt.Errorf("mostro: funded %s has unexpected wallet status %s", purpose, status)
		}
	}
	return m.Store.SaveMostroSession(ctx, s, nil)
}

func (m *Manager) review(ctx context.Context, s *Session, cause error) error {
	if s.State != "needs_review" {
		s.State = "needs_review"
		s.LastError = cause.Error()
	}
	if err := m.Store.SaveMostroSession(ctx, s, nil); err != nil {
		return fmt.Errorf("%v; persist review: %w", cause, err)
	}
	return fmt.Errorf("mostro: %w", cause)
}
