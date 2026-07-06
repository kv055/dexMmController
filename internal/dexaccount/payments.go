package dexaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"commons/telegram"
	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/bisq2"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
)

// paymentStyle classifies how a venue exposes the counterparty's fiat details — the "two taker
// styles": structured (carried in the trade object) vs chat (a free-form conversation).
type paymentStyle int

const (
	styleUnknown paymentStyle = iota
	// styleStructured: details are in the trade object (Bisq 1's contract; HodlHodl contract).
	styleStructured
	// styleChat: details are exchanged via the trade chat (Robosats; Bisq 2 Easy).
	styleChat
)

func styleFor(exchange string) paymentStyle {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexBisq:
		return styleStructured
	case db.DexRoboSats, db.DexBisq2:
		return styleChat
	case db.DexHodlHodl:
		// HodlHodl payment details live in the contract; chat is for operator relay only.
		return styleStructured
	default:
		return styleUnknown
	}
}

// PaymentResolver fetches the counterparty's fiat/bank details for a taken offer. Structured
// venues (Bisq, HodlHodl) read the trade contract in one shot; chat venues (Robosats, Bisq2)
// run a short conversation: send one templated request and capture the counterparty's reply.
type PaymentResolver struct {
	repo     *db.TransactionRepository
	bisq     *bisq.Client
	bisq2    *bisq2.Client
	hodlhodl *hodlhodl.Client
	robosats *robosats.Client
}

// NewPaymentResolver creates a PaymentResolver backed by the configured DEX clients.
func NewPaymentResolver(
	repo *db.TransactionRepository,
	bisqClient *bisq.Client,
	robosatsClient *robosats.Client,
	bisq2Client *bisq2.Client,
	hodlHodlClient *hodlhodl.Client,
) *PaymentResolver {
	return &PaymentResolver{
		repo:     repo,
		bisq:     bisqClient,
		bisq2:    bisq2Client,
		hodlhodl: hodlHodlClient,
		robosats: robosatsClient,
	}
}

// IsStructuredVenue reports whether a venue exposes details in the trade object (so the
// one-shot FetchCounterparty applies) rather than via chat.
func (p *PaymentResolver) IsStructuredVenue(exchange string) bool {
	return styleFor(exchange) == styleStructured
}

// TradeChatMessage is a venue-agnostic trade-chat message (alias of commons/telegram.Message).
type TradeChatMessage = telegram.Message

// TradeChat is a venue's trade-chat transport (alias of commons/telegram.TradeChat).
type TradeChat = telegram.TradeChat

// ---------- Structured venues (Bisq) ----------

type counterpartyEnvelope struct {
	Exchange  string          `json:"exchange"`
	Source    string          `json:"source"`
	FetchedAt time.Time       `json:"fetched_at"`
	Raw       json.RawMessage `json:"raw"`
}

// FetchCounterparty fetches a structured venue's counterparty payment details.
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

// ---------- Chat venues (Robosats; Bisq2) ----------

// ChatRequest identifies the trade chat to drive and the amount we expect to receive.
type ChatRequest struct {
	Exchange         string
	OrderID          string
	ExpectedAmount   string
	ExpectedCurrency string
}

const paymentRequestMarker = "match what you provide in this chat"

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

type chatPaymentEnvelope struct {
	Exchange             string              `json:"exchange"`
	Source               string              `json:"source"`
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

// CaptureChatPayment drives the chat conversation for a taken offer.
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

// TradeChatFor resolves the venue chat transport (used by dexnotify relay registration).
func (p *PaymentResolver) TradeChatFor(ctx context.Context, exchange string) (TradeChat, error) {
	return p.tradeChat(ctx, exchange)
}

func captureFromChat(ctx context.Context, chat TradeChat, req ChatRequest) (json.RawMessage, error) {
	msgs, err := chat.Messages(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}
	if !requestAlreadySent(msgs) {
		if err := chat.SendMessage(ctx, req.OrderID, paymentRequestMessage(req.ExpectedAmount, req.ExpectedCurrency)); err != nil {
			return nil, fmt.Errorf("dexaccount: send payment request: %w", err)
		}
		if refreshed, rerr := chat.Messages(ctx, req.OrderID); rerr == nil {
			msgs = refreshed
		}
	}
	return marshalChatEnvelope(req, msgs)
}

func (p *PaymentResolver) tradeChat(ctx context.Context, exchange string) (TradeChat, error) {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexRoboSats:
		if p.robosats == nil {
			return nil, fmt.Errorf("robosats client is not configured")
		}
		return robosatsChat{client: p.roboSatsClient(ctx)}, nil
	case db.DexBisq2:
		if p.bisq2 == nil {
			return nil, fmt.Errorf("bisq2 client is not configured")
		}
		return bisq2.TradeChat{Client: p.bisq2}, nil
	case db.DexHodlHodl:
		if p.hodlhodl == nil {
			return nil, fmt.Errorf("hodlhodl client is not configured")
		}
		return hodlhodlChat{client: p.hodlhodl}, nil
	default:
		return nil, fmt.Errorf("dexaccount: chat flow not implemented for exchange %q", exchange)
	}
}

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

type hodlhodlChat struct {
	client *hodlhodl.Client
}

func (c hodlhodlChat) SendMessage(ctx context.Context, orderID, text string) error {
	return c.client.SendContractMessage(ctx, orderID, text)
}

func (c hodlhodlChat) Messages(ctx context.Context, orderID string) ([]TradeChatMessage, error) {
	msgs, err := c.client.GetContractMessages(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]TradeChatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, TradeChatMessage{Time: m.SentAt, Sender: m.Author, Text: m.Text})
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

func requestAlreadySent(msgs []TradeChatMessage) bool {
	for _, m := range msgs {
		if m.Outbound || strings.Contains(m.Text, paymentRequestMarker) {
			return true
		}
	}
	return false
}

func isInbound(m TradeChatMessage) bool {
	return !m.Outbound && !strings.Contains(m.Text, paymentRequestMarker)
}

func (p *PaymentResolver) roboSatsClient(ctx context.Context) *robosats.Client {
	if token := robosatsTokenForRepo(ctx, p.repo); token != "" {
		return p.robosats.WithAuthToken(token)
	}
	return p.robosats
}
