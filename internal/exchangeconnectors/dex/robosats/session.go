package robosats

import (
	"context"
	"fmt"
	"time"

	lnd "dexMmController/internal/wallethub"
)

// TakeRequest binds coordinator, order, credential and spend ceilings to one transaction.
type TakeRequest struct {
	RequestID        uint64       `json:"request_id"`
	CoordinatorID    string       `json:"coordinator_id"`
	OrderID          string       `json:"order_id"`
	FiatAmount       string       `json:"fiat_amount"`
	FiatCurrency     string       `json:"fiat_currency"`
	PaymentMethod    string       `json:"payment_method"`
	MaxBondSat       int64        `json:"max_bond_sat"`
	MaxEscrowSat     int64        `json:"max_escrow_sat"`
	RoutingFeeSat    int64        `json:"routing_fee_sat"`
	BondFeeSat       int64        `json:"bond_fee_sat"`
	EscrowFeeSat     int64        `json:"escrow_fee_sat"`
	CoordinatorURL   string       `json:"coordinator_url,omitempty"`
	NodePubKey       string       `json:"node_pubkey,omitempty"`
	CredentialID     int64        `json:"credential_id"`
	QuoteID          string       `json:"quote_id,omitempty"`
	Terms            *NativeTerms `json:"native_terms,omitempty"`
	TokenFingerprint string       `json:"token_fingerprint,omitempty"`
}

// Payment is one hold invoice (bond or escrow) with durable LND tracking.
type Payment struct {
	Purpose    string    `json:"purpose"`
	Invoice    string    `json:"invoice"`
	Hash       string    `json:"hash"`
	AmountSat  int64     `json:"amount_sat"`
	AmountMsat int64     `json:"amount_msat"`
	Status     string    `json:"status"`
	Submitted  bool      `json:"submitted"`
	Attempted  bool      `json:"attempted"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Session is the durable RoboSats execution record for one transaction.
type Session struct {
	ID                  int64               `json:"id"`
	Request             TakeRequest         `json:"request"`
	Phase               string              `json:"phase"`
	CoordinatorStatus   int                 `json:"coordinator_status"`
	PublicKey           string              `json:"public_key,omitempty"`
	EncPrivKey          string              `json:"encrypted_private_key,omitempty"`
	RobotNick           string              `json:"robot_nick,omitempty"`
	Payments            map[string]*Payment `json:"payments"`
	ChatTemplate        string              `json:"chat_template,omitempty"`
	ChatSent            bool                `json:"chat_sent"`
	Funded              bool                `json:"funded"`
	LastError           string              `json:"last_error,omitempty"`
	Deadline            time.Time           `json:"deadline,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	RecoveryEncrypted   string              `json:"recovery_encrypted,omitempty"`
	ChatError           string              `json:"chat_error,omitempty"`
	PreparationComplete bool                `json:"preparation_complete"`
}

// Store mutations commit before returning; network I/O never runs inside an
// uncommitted database transaction.
type Store interface {
	LockRoboSats(context.Context, uint64) (func(), error)
	LoadRoboSatsSession(context.Context, uint64) (*Session, error)
	CreateRoboSatsSession(context.Context, *Session) (*Session, error)
	SaveRoboSatsSession(context.Context, *Session) error
}

// Wallet is the hold-invoice surface used for bond and escrow.
type Wallet interface {
	DecodeInvoice(context.Context, string) (*lnd.DecodedInvoice, error)
	HoldPayment(context.Context, string, string, int64) (string, error)
	TrackHoldPayment(context.Context, string) (string, error)
}

// OrderAPI is the coordinator HTTP surface used by the state machine.
type OrderAPI interface {
	GetRobot(context.Context) (Robot, error)
	TakeOfferWithAmount(context.Context, string, string) (*Position, error)
	GetPosition(context.Context, string) (*Position, error)
	GetChatMessages(context.Context, string) (*ChatThread, error)
	SendChatMessage(context.Context, string, string) error
	Identity() *Identity
}

type TakeResult struct {
	SessionID   int64
	OrderID     string
	Funded      bool
	Phase       string
	PaymentHash string
	AmountSat   int64
}

func (s *Session) TradeRef() string {
	return fmt.Sprintf("%s:%s", s.Request.CoordinatorID, s.Request.OrderID)
}

func (s *Session) takeResult() *TakeResult {
	r := &TakeResult{SessionID: s.ID, OrderID: s.Request.OrderID, Funded: s.Funded, Phase: s.Phase}
	if p := s.Payments["escrow"]; p != nil {
		r.PaymentHash = p.Hash
		r.AmountSat = p.AmountSat
	}
	return r
}
