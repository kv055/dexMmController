package dexaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
)

// paymentStyle classifies how a venue exposes the counterparty's fiat details — the "two taker
// styles": structured (carried in the trade object) vs chat (a free-form conversation).
type paymentStyle int

const (
	styleUnknown paymentStyle = iota
	// styleStructured: details are in the trade object (Bisq 1's contract).
	styleStructured
	// styleChat: details are exchanged via the trade chat (Robosats; Bisq 2 Easy later).
	styleChat
)

func styleFor(exchange string) paymentStyle {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexBisq:
		return styleStructured
	case db.DexRoboSats:
		return styleChat
	default:
		return styleUnknown
	}
}

// PaymentResolver fetches the counterparty's fiat/bank details for a taken offer. Structured
// venues (Bisq) read the trade contract in one shot; chat venues (Robosats, later Bisq 2 Easy)
// run a short conversation: send one templated request and capture the counterparty's reply.
type PaymentResolver struct {
	repo     *db.TransactionRepository
	bisq     *bisq.Client
	robosats *robosats.Client
}

// NewPaymentResolver creates a PaymentResolver backed by the configured DEX clients.
func NewPaymentResolver(repo *db.TransactionRepository, bisqClient *bisq.Client, robosatsClient *robosats.Client) *PaymentResolver {
	return &PaymentResolver{repo: repo, bisq: bisqClient, robosats: robosatsClient}
}

// IsStructuredVenue reports whether a venue exposes details in the trade object (so the
// one-shot FetchCounterparty applies) rather than via chat.
func (p *PaymentResolver) IsStructuredVenue(exchange string) bool {
	return styleFor(exchange) == styleStructured
}

// ---------- Structured venues (Bisq) ----------

// counterpartyEnvelope wraps a structured venue payload stored verbatim in
// transactions.counterparty_payment_info. Raw is the venue payload as-is (Bisq payment-account
// JSON); payment types vary too widely to model.
type counterpartyEnvelope struct {
	Exchange  string          `json:"exchange"`
	Source    string          `json:"source"` // "structured"
	FetchedAt time.Time       `json:"fetched_at"`
	Raw       json.RawMessage `json:"raw"`
}

// FetchCounterparty fetches a structured venue's counterparty payment details (Bisq: the trade
// contract) and returns the storage envelope. Chat venues do not use this — they go through
// CaptureChatPayment — so the chat case returns an error.
func (p *PaymentResolver) FetchCounterparty(ctx context.Context, exchange string, execution map[string]any) (json.RawMessage, error) {
	switch styleFor(exchange) {
	case styleStructured:
		return p.fetchBisq(ctx, exchange, execution)
	case styleChat:
		return nil, fmt.Errorf("dexaccount: %q uses the chat flow; call CaptureChatPayment", exchange)
	default:
		return nil, fmt.Errorf("dexaccount: counterparty payment fetch unsupported for exchange %q", exchange)
	}
}

func (p *PaymentResolver) fetchBisq(ctx context.Context, exchange string, execution map[string]any) (json.RawMessage, error) {
	if p.bisq == nil {
		return nil, fmt.Errorf("bisq client is not configured")
	}
	tradeID := StringField(execution, "trade_id")
	if tradeID == "" {
		return nil, fmt.Errorf("bisq: trade_id missing from execution")
	}
	raw, err := p.bisq.GetCounterpartyPayment(ctx, tradeID)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(counterpartyEnvelope{
		Exchange:  exchange,
		Source:    "structured",
		FetchedAt: time.Now().UTC(),
		Raw:       raw,
	})
	if err != nil {
		return nil, fmt.Errorf("dexaccount: marshal counterparty payment envelope: %w", err)
	}
	return out, nil
}

// ---------- Chat venues (Robosats; Bisq 2 Easy later) ----------

// TradeChatMessage is a venue-agnostic trade-chat message. Outbound marks messages we sent;
// venues that cannot reliably attribute the sender leave it false (the request marker is used
// as the fallback to distinguish our own message from the counterparty's).
type TradeChatMessage struct {
	Time     string // venue-provided timestamp, kept verbatim
	Sender   string // sender handle/nick, if the venue exposes it
	Outbound bool   // true if we sent it
	Text     string // decrypted/plaintext message body
}

// TradeChat is a venue's trade-chat transport. Robosats implements it now; Bisq 2 Easy
// (REST at BISQ2_REST_HOST) will implement the same interface later.
type TradeChat interface {
	SendMessage(ctx context.Context, orderID, text string) error
	Messages(ctx context.Context, orderID string) ([]TradeChatMessage, error)
}

// ChatRequest identifies the trade chat to drive and the amount we expect to receive (so the
// templated request can state it, and the fiat controller can match against it).
type ChatRequest struct {
	Exchange         string
	OrderID          string
	ExpectedAmount   string
	ExpectedCurrency string
}

// paymentRequestMarker is a stable phrase present in our request template. We only ever send
// that one message, so any chat message containing it is ours — used for both send-once
// idempotency and to exclude our own message from the captured (inbound-only) transcript.
const paymentRequestMarker = "match what you provide in this chat"

// paymentRequestTemplate is the single automated message asking the counterparty for their
// fiat details and stating the release/dispute policy. The %s is the expected amount+currency.
const paymentRequestTemplate = `Hi! To release the Bitcoin I need to confirm your payment.

Please reply here with:
• The full name on your account
• The account / handle / IBAN the payment will come from
• The payment method

I will release the Bitcoin only after I receive exactly %s and the sender name, handle and payment details match what you provide in this chat. If they don't match, I'll have to open a dispute. Thank you!`

func paymentRequestMessage(amount, currency string) string {
	amt := strings.TrimSpace(strings.TrimSpace(amount) + " " + strings.TrimSpace(currency))
	if amt == "" {
		amt = "the agreed amount"
	}
	return fmt.Sprintf(paymentRequestTemplate, amt)
}

// chatPaymentEnvelope is the inbound-only view stored in counterparty_payment_info for chat
// venues: only the counterparty's messages plus the expected amount/currency and their handle.
// Our own outbound messages are dropped. The fiat controller matches these against the real
// payment.
type chatPaymentEnvelope struct {
	Exchange             string              `json:"exchange"`
	Source               string              `json:"source"` // "chat"
	FetchedAt            time.Time           `json:"fetched_at"`
	OrderID              string              `json:"order_id"`
	Expected             chatExpected        `json:"expected"`
	CounterpartyHandle   string              `json:"counterparty_handle,omitempty"`
	CounterpartyMessages []chatMessageRecord `json:"counterparty_messages"`
}

type chatExpected struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type chatMessageRecord struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// CaptureChatPayment drives the chat conversation for a taken offer: it sends the templated
// payment-details request once (idempotent — skipped if our message is already in the thread),
// then returns the inbound-only envelope of the counterparty's replies for storage. Matching,
// release and dispute are the fiat controller's job.
func (p *PaymentResolver) CaptureChatPayment(ctx context.Context, req ChatRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return nil, fmt.Errorf("dexaccount: order_id is required for chat capture")
	}
	chat, err := p.tradeChat(ctx, req.Exchange)
	if err != nil {
		return nil, err
	}
	return captureFromChat(ctx, chat, req)
}

// captureFromChat is the venue-agnostic conversation core: send the request once, then build
// the inbound-only envelope. Split out from CaptureChatPayment so it can be tested with a fake
// TradeChat.
func captureFromChat(ctx context.Context, chat TradeChat, req ChatRequest) (json.RawMessage, error) {
	msgs, err := chat.Messages(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}
	if !requestAlreadySent(msgs) {
		if err := chat.SendMessage(ctx, req.OrderID, paymentRequestMessage(req.ExpectedAmount, req.ExpectedCurrency)); err != nil {
			return nil, fmt.Errorf("dexaccount: send payment request: %w", err)
		}
		// Re-read so any reply that landed meanwhile is captured (best-effort).
		if refreshed, rerr := chat.Messages(ctx, req.OrderID); rerr == nil {
			msgs = refreshed
		}
	}
	return marshalChatEnvelope(req, msgs)
}

// tradeChat returns the venue's chat transport. Robosats now; Bisq 2 Easy later.
func (p *PaymentResolver) tradeChat(ctx context.Context, exchange string) (TradeChat, error) {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexRoboSats:
		if p.robosats == nil {
			return nil, fmt.Errorf("robosats client is not configured")
		}
		return robosatsChat{client: p.roboSatsClient(ctx)}, nil
	default:
		return nil, fmt.Errorf("dexaccount: chat flow not implemented for exchange %q", exchange)
	}
}

// robosatsChat adapts robosats.Client to the venue-agnostic TradeChat. Outbound is left false;
// our own message is distinguished by the request marker (we only ever send that one message).
type robosatsChat struct {
	client *robosats.Client
}

func (c robosatsChat) SendMessage(ctx context.Context, orderID, text string) error {
	return c.client.SendChatMessage(ctx, orderID, text)
}

func (c robosatsChat) Messages(ctx context.Context, orderID string) ([]TradeChatMessage, error) {
	thread, err := c.client.GetChatMessages(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]TradeChatMessage, 0, len(thread.Messages))
	for _, m := range thread.Messages {
		out = append(out, TradeChatMessage{Time: m.Time, Sender: m.Nick, Text: m.Plaintext})
	}
	return out, nil
}

func marshalChatEnvelope(req ChatRequest, msgs []TradeChatMessage) (json.RawMessage, error) {
	env := chatPaymentEnvelope{
		Exchange:             req.Exchange,
		Source:               "chat",
		FetchedAt:            time.Now().UTC(),
		OrderID:              req.OrderID,
		Expected:             chatExpected{Amount: strings.TrimSpace(req.ExpectedAmount), Currency: strings.TrimSpace(req.ExpectedCurrency)},
		CounterpartyMessages: []chatMessageRecord{},
	}
	for _, m := range msgs {
		if !isInbound(m) {
			continue
		}
		if env.CounterpartyHandle == "" && strings.TrimSpace(m.Sender) != "" {
			env.CounterpartyHandle = strings.TrimSpace(m.Sender)
		}
		env.CounterpartyMessages = append(env.CounterpartyMessages, chatMessageRecord{Time: m.Time, Text: m.Text})
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("dexaccount: marshal chat payment envelope: %w", err)
	}
	return out, nil
}

// requestAlreadySent reports whether our templated request is already in the thread.
func requestAlreadySent(msgs []TradeChatMessage) bool {
	for _, m := range msgs {
		if m.Outbound || strings.Contains(m.Text, paymentRequestMarker) {
			return true
		}
	}
	return false
}

// isInbound reports whether a message is the counterparty's (not our own request).
func isInbound(m TradeChatMessage) bool {
	return !m.Outbound && !strings.Contains(m.Text, paymentRequestMarker)
}

// roboSatsClient returns the Robosats client bound to the latest stored robot token when one is
// available (same lookup as Taker/Releaser), falling back to the configured client.
func (p *PaymentResolver) roboSatsClient(ctx context.Context) *robosats.Client {
	if token := robosatsTokenForRepo(ctx, p.repo); token != "" {
		return p.robosats.WithAuthToken(token)
	}
	return p.robosats
}
