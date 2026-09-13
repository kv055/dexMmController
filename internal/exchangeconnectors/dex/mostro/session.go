package mostro

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	lnd "dexMmController/internal/wallethub"
	"github.com/nbd-wtf/go-nostr"
)

// TakeRequest fixes the node, signed quote and spend ceilings for the lifetime
// of a transaction. Retry callers cannot silently change these terms.
type TakeRequest struct {
	LightningNode string `json:"lightning_node"`
	RequestID     uint64 `json:"request_id"`
	Node          string `json:"node"`
	OrderID       string `json:"order_id"`
	SourceEventID string `json:"source_event_id"`
	Protocol      int    `json:"protocol"`
	FiatAmount    int64  `json:"fiat_amount"`
	FiatCurrency  string `json:"fiat_currency"`
	PaymentMethod string `json:"payment_method"`
	MaxEscrowSat  int64  `json:"max_escrow_sat"`
	MaxBondSat    int64  `json:"max_bond_sat"`
	RoutingFeeSat int64  `json:"routing_fee_sat"`
}
type Payment struct {
	Purpose   string `json:"purpose"`
	Invoice   string `json:"invoice"`
	Hash      string `json:"hash"`
	AmountSat int64  `json:"amount_sat"`
	Status    string `json:"status"`
}
type Session struct {
	IdentityPubkey    string              `json:"identity_pubkey"`
	ID                int64               `json:"id"`
	IdentityID        int64               `json:"identity_id"`
	TradeIndex        int64               `json:"trade_index"`
	TradePubkey       string              `json:"trade_pubkey"`
	EncryptedMnemonic []byte              `json:"-"`
	Request           TakeRequest         `json:"request"`
	CreatedAt         time.Time           `json:"created_at"`
	Privacy           bool                `json:"privacy"`
	State             string              `json:"state"`
	PeerPubkey        string              `json:"peer_pubkey,omitempty"`
	Funded            bool                `json:"funded"`
	FiatSent          bool                `json:"fiat_sent"`
	Released          bool                `json:"released"`
	BondRequired      bool                `json:"bond_required"`
	Payments          map[string]*Payment `json:"payments"`
	Seen              map[string]bool     `json:"seen"`
	LastError         string              `json:"last_error,omitempty"`
}

// Store mutations commit before returning; network effects must never execute
// inside an uncommitted database transaction. Lock serializes one trade across
// workers/processes, including release and restart reconciliation.
type Store interface {
	AuthorizeMostroRelease(context.Context, uint64) error
	LoadMostroChatEvents(context.Context, int64) ([]nostr.Event, error)
	SaveMostroChatEvent(context.Context, int64, nostr.Event, string) error
	LockMostro(context.Context, uint64) (func(), error)
	LoadMostroSession(context.Context, uint64) (*Session, error)
	CreateMostroSession(context.Context, string, []byte, func([]byte, int64) (*Session, error)) (*Session, error)
	SaveMostroSession(context.Context, *Session, *nostr.Event) error
	MostroOutbox(context.Context, int64, string, func() (nostr.Event, error)) (nostr.Event, error)
	AckMostroOutbox(context.Context, string) error
}
type Wallet interface {
	DecodeInvoice(context.Context, string) (*lnd.DecodedInvoice, error)
	HoldPayment(context.Context, string, string, int64) (string, error)
	TrackHoldPayment(context.Context, string) (string, error)
}
type TakeResult struct {
	EventID        string
	Action         string
	PaymentInvoice string
	PaymentHash    string
	AmountSat      int64
	Funded         bool
	SessionID      int64
}

func (s *Session) takeResult() *TakeResult {
	p := s.Payments["escrow"]
	r := &TakeResult{Action: s.State, Funded: s.Funded, SessionID: s.ID}
	if p != nil {
		r.PaymentInvoice = p.Invoice
		r.PaymentHash = p.Hash
		r.AmountSat = p.AmountSat
	}
	return r
}
func encodePayload(value any) json.RawMessage { b, _ := json.Marshal(value); return b }

func (s *Session) TradeRef() string {
	return fmt.Sprintf("%s:%s:%d", s.Request.Node, s.Request.OrderID, s.Request.RequestID)
}
