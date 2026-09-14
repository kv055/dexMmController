package mostro

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func savedSession(t *testing.T, m *Manager, store *memoryStore, n *simulatedNode, req TakeRequest) *Session {
	t.Helper()
	n.failPublish = true
	if _, err := m.Take(context.Background(), req); err == nil {
		t.Fatal("expected ambiguous publish")
	}
	n.failPublish = false
	s, err := store.LoadMostroSession(context.Background(), req.RequestID)
	if err != nil || s == nil {
		t.Fatalf("session: %v", err)
	}
	n.publishIDs = nil
	return s
}
func addHistory(t *testing.T, n *simulatedNode, s *Session, action string, payload json.RawMessage) {
	t.Helper()
	n.send(t, s.TradePubkey, s.Request, action, payload)
	n.history = append(n.history, <-n.conn.events)
}
func TestRecoveryReplaysCancellationBeforePaymentOrRelease(t *testing.T) {
	for _, mode := range []string{"saved-intent", "history-invoice", "live-cancellation", "release", "incomplete-replay"} {
		t.Run(mode, func(t *testing.T) {
			m, store, n, w, req := fixture(t, false)
			s := savedSession(t, m, store, n, req)
			if mode == "history-invoice" {
				addHistory(t, n, s, "pay-invoice", invoicePayload("escrow"))
			} else {
				if err := m.apply(context.Background(), s, MessageKind{Action: "pay-invoice", Payload: invoicePayload("escrow")}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "release" {
				s.Funded = true
				s.State = "funded"
				store.authorized = true
				w.statuses[strings.Repeat("a", 64)] = "IN_FLIGHT"
			}
			if err := store.SaveMostroSession(context.Background(), s, nil); err != nil {
				t.Fatal(err)
			}
			if mode == "live-cancellation" {
				n.onPublish = func(e nostr.Event) { n.send(t, e.PubKey, req, "canceled", json.RawMessage("null")) }
			} else if mode == "incomplete-replay" {
				n.openError = errors.New("missing EOSE")
			} else {
				addHistory(t, n, s, "canceled", json.RawMessage("null"))
			}
			var err error
			if mode == "release" {
				err = m.Release(context.Background(), req.RequestID, s.TradeRef())
			} else {
				_, err = m.Take(context.Background(), req)
			}
			if err == nil {
				t.Fatal("unsafe recovery succeeded")
			}
			if len(w.calls) != 0 {
				t.Fatalf("paid before replaying cancellation: %v", w.calls)
			}
			if mode != "live-cancellation" && len(n.publishIDs) != 0 {
				t.Fatal("published before replay completed safely")
			}
		})
	}
}
func TestReviewSurvivesDelayedAcceptanceAndPolling(t *testing.T) {
	m, store, n, _, req := fixture(t, false)
	s := savedSession(t, m, store, n, req)
	if err := m.apply(context.Background(), s, MessageKind{Action: "pay-invoice", Payload: invoicePayload("escrow")}); err != nil {
		t.Fatal(err)
	}
	addHistory(t, n, s, "dispute-initiated-by-peer", json.RawMessage("null"))
	addHistory(t, n, s, "waiting-buyer-invoice", json.RawMessage("null"))
	if err := m.poll(context.Background(), s); err == nil {
		t.Fatal("poll concealed review")
	}
	cause := s.LastError
	if s.State != "needs_review" || !strings.Contains(cause, "dispute") || s.Funded {
		t.Fatalf("review lost: %+v", s)
	}
	for _, action := range []string{"buyer-invoice-accepted", "pay-invoice", "released", "canceled"} {
		_ = m.apply(context.Background(), s, MessageKind{Action: action, Payload: invoicePayload("escrow")})
		if s.State != "needs_review" || s.LastError != cause || s.Released {
			t.Fatal("delayed event cleared original review")
		}
	}
	store.authorized = true
	if err := m.Release(context.Background(), req.RequestID, s.TradeRef()); err == nil {
		t.Fatal("released review session")
	}
	if len(n.publishIDs) != 0 {
		t.Fatal("review published release")
	}
}
func TestReleasedSessionCannotRegress(t *testing.T) {
	m, _, _, _, _ := fixture(t, false)
	s := &Session{State: "released", Released: true}
	for _, action := range []string{"pay-invoice", "pay-bond-invoice", "waiting-buyer-invoice", "hold-invoice-payment-accepted"} {
		if err := m.apply(context.Background(), s, MessageKind{Action: action}); err != nil {
			t.Fatal(err)
		}
		if s.State != "released" || !s.Released || len(s.Payments) != 0 {
			t.Fatal("settled session reopened")
		}
	}
}
func setTag(e *nostr.Event, key string, values ...string) {
	tags := nostr.Tags{}
	for _, tag := range e.Tags {
		if tag[0] != key {
			tags = append(tags, tag)
		}
	}
	if len(values) != 0 {
		tags = append(tags, append(nostr.Tag{key}, values...))
	}
	e.Tags = tags
}
func TestPreflightChecksSignedEconomicsBeforeAllocating(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*simulatedNode, *TakeRequest)
		valid  bool
	}{
		{"exact budgets", func(n *simulatedNode, r *TakeRequest) { r.MaxEscrowSat = 10015 }, true},
		{"escrow too small", func(n *simulatedNode, r *TakeRequest) { r.MaxEscrowSat = 10014 }, false},
		{"bond too small", func(n *simulatedNode, r *TakeRequest) { r.MaxBondSat = 499 }, false},
		{"both one sat", func(n *simulatedNode, r *TakeRequest) { r.MaxEscrowSat = 1; r.MaxBondSat = 1 }, false},
		{"percentage bond", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "bond_amount_pct", "0.06"); r.MaxBondSat = 599 }, false},
		{"zero fee", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "fee", "0"); r.MaxEscrowSat = 10000 }, true},
		{"fractional fee conservative rounding", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "fee", "0.00001"); r.MaxEscrowSat = 10000 }, false},
		{"floating", func(n *simulatedNode, r *TakeRequest) { setTag(&n.order, "amt", "0") }, false},
		{"range", func(n *simulatedNode, r *TakeRequest) { setTag(&n.order, "fa", "10", "100") }, false},
		{"missing amount", func(n *simulatedNode, r *TakeRequest) { setTag(&n.order, "amt") }, false},
		{"missing bond floor", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "bond_base_amount_sats") }, false},
		{"missing bond percentage", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "bond_amount_pct") }, false},
		{"bad bond percentage", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "bond_amount_pct", "NaN") }, false},
		{"unknown bond policy", func(n *simulatedNode, r *TakeRequest) { setTag(&n.node, "bond_apply_to", "other") }, false},
		{"overflow", func(n *simulatedNode, r *TakeRequest) {
			setTag(&n.order, "amt", "9223372036854775807")
			r.MaxEscrowSat = 9223372036854775807
		}, false},
		{"duplicate fee", func(n *simulatedNode, r *TakeRequest) { n.node.Tags = append(n.node.Tags, nostr.Tag{"fee", "0"}) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store, n, w, req := fixture(t, true)
			setTag(&n.order, "amt", "10000")
			req.MaxEscrowSat = 10015
			tc.change(n, &req)
			if err := n.order.Sign(n.key); err != nil {
				t.Fatal(err)
			}
			if err := n.node.Sign(n.key); err != nil {
				t.Fatal(err)
			}
			req.SourceEventID = n.order.ID
			err := m.Check(context.Background(), req)
			if (err == nil) != tc.valid {
				t.Fatalf("Check error=%v, valid=%v", err, tc.valid)
			}
			if !tc.valid {
				if _, err = m.Take(context.Background(), req); err == nil {
					t.Fatal("Take accepted invalid budgets")
				}
			}
			if store.allocations != 0 || len(n.publishIDs) != 0 || len(w.calls) != 0 {
				t.Fatal("preflight caused side effects")
			}
		})
	}
}
func TestReconcileRequiresDaemonAndWalletSettlementWithoutCommands(t *testing.T) {
	for _, tc := range []struct {
		name, escrow, bond          string
		released, complete, wantErr bool
	}{
		{"still funded", "IN_FLIGHT", "IN_FLIGHT", false, false, false},
		{"daemon only", "IN_FLIGHT", "IN_FLIGHT", true, false, true},
		{"wallet only", "SUCCEEDED", "FAILED", false, false, true},
		{"bond still locked", "SUCCEEDED", "IN_FLIGHT", true, false, true},
		{"bond slashed", "SUCCEEDED", "SUCCEEDED", true, false, true},
		{"complete", "SUCCEEDED", "FAILED", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store, n, w, req := fixture(t, true)
			s := savedSession(t, m, store, n, req)
			for _, purpose := range []string{"bond", "escrow"} {
				action := "pay-invoice"
				if purpose == "bond" {
					action = "pay-bond-invoice"
				}
				if err := m.apply(context.Background(), s, MessageKind{Action: action, Payload: invoicePayload(purpose)}); err != nil {
					t.Fatal(err)
				}
			}
			s.Funded = true
			s.State = "funded"
			if err := store.SaveMostroSession(context.Background(), s, nil); err != nil {
				t.Fatal(err)
			}
			if tc.released {
				addHistory(t, n, s, "released", json.RawMessage("null"))
			}
			w.statuses[strings.Repeat("a", 64)] = tc.escrow
			w.statuses[strings.Repeat("b", 64)] = tc.bond
			complete, err := m.Reconcile(context.Background(), req.RequestID, s.TradeRef())
			if complete != tc.complete || (err != nil) != tc.wantErr {
				t.Fatalf("complete=%v err=%v", complete, err)
			}
			if len(n.publishIDs) != 0 || len(w.calls) != 0 {
				t.Fatal("read-only reconciliation issued commands")
			}
		})
	}
}

func TestCancellationDuringBondAttemptPreventsEscrow(t *testing.T) {
	m, store, n, w, req := fixture(t, true)
	s := savedSession(t, m, store, n, req)
	addHistory(t, n, s, "pay-bond-invoice", invoicePayload("bond"))
	addHistory(t, n, s, "pay-invoice", invoicePayload("escrow"))
	w.onPay = func(string) { n.send(t, s.TradePubkey, req, "canceled", json.RawMessage("null")) }
	if _, err := m.Take(context.Background(), req); err == nil {
		t.Fatal("cancellation ignored")
	}
	if len(w.calls) != 1 || w.calls[0] != strings.Repeat("b", 64) {
		t.Fatalf("escrow paid after cancellation: %v", w.calls)
	}
}
func TestSettlementRequiresRecordedBond(t *testing.T) {
	m, _, _, _, _ := fixture(t, true)
	s := &Session{BondRequired: true, Released: true}
	if err := m.confirmSettlement(context.Background(), s); err == nil {
		t.Fatal("missing required bond accepted")
	}
}
