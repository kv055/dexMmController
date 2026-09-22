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
	"dexMmController/internal/tradechat"
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
	case db.DexRoboSats, db.DexBisq2, db.DexMostro:
		return styleChat
	case db.DexHodlHodl:
		// HodlHodl payment details live in the contract; chat is for operator relay only.
		return styleStructured
	default:
		return styleUnknown
	}
}

// ChatTemplateStore loads the active chat request template for a payment method.
type ChatTemplateStore interface {
	GetChatTemplate(ctx context.Context, exchange, paymentMethodName string) (string, error)
}

// PaymentResolver fetches the counterparty's fiat/bank details for a taken offer. Structured
// venues (Bisq, HodlHodl) read the trade contract in one shot; chat venues (Robosats, Bisq2)
// run a short conversation: send one templated request and capture the counterparty's reply.
type PaymentResolver struct {
	mostro          TradeChat
	repo            *db.TransactionRepository
	templates       ChatTemplateStore
	bisq            *bisq.Client
	bisq2           *bisq2.Client
	hodlhodl        *hodlhodl.Client
	robosats        *robosats.Client
	roboCoordinator *robosats.Coordinator
	robosatsDial    func(coordinatorID, token string) (*robosats.Client, error)
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
		repo:      repo,
		templates: repo,
		bisq:      bisqClient,
		bisq2:     bisq2Client,
		hodlhodl:  hodlHodlClient,
		robosats:  robosatsClient,
	}
}

func (p *PaymentResolver) WithMostro(chat TradeChat) *PaymentResolver { p.mostro = chat; return p }

func (p *PaymentResolver) WithRoboSatsCoordinator(c *robosats.Coordinator) *PaymentResolver {
	p.roboCoordinator = c
	return p
}

func (p *PaymentResolver) WithRoboSatsDial(dial func(coordinatorID, token string) (*robosats.Client, error)) *PaymentResolver {
	p.robosatsDial = dial
	return p
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
	// PaymentMethod is the canonical catalog name selected for this trade. Used to
	// pick the custom chat template from payment_method_chat_templates.
	PaymentMethod  string
	TradeDirection string // bid or ask; buy/sell are accepted for legacy callers.
}

const paymentRequestMarker = tradechat.PaymentRequestMarker

func paymentRequestMessage(template, amount, currency string) string {
	return tradechat.RenderPaymentRequest(template, amount, currency)
}

func (p *PaymentResolver) paymentRequestVariables(ctx context.Context, paymentMethodName string) (map[string]string, error) {
	if p.repo == nil {
		return nil, nil
	}
	return p.repo.GetChatTemplateVariables(ctx, paymentMethodName)
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
	var template string
	if p.templates != nil {
		if directional, ok := p.templates.(interface {
			GetChatTemplateForDirection(context.Context, string, string, string) (string, error)
		}); ok {
			template, err = directional.GetChatTemplateForDirection(ctx, req.Exchange, req.PaymentMethod, req.TradeDirection)
		} else {
			template, err = p.templates.GetChatTemplate(ctx, req.Exchange, req.PaymentMethod)
		}
		if err != nil {
			return nil, fmt.Errorf("dexaccount: load chat template: %w", err)
		}
	}
	variables, err := p.paymentRequestVariables(ctx, req.PaymentMethod)
	if err != nil {
		return nil, fmt.Errorf("dexaccount: load payment template variables: %w", err)
	}
	template = renderPaymentRequest(template, req.ExpectedAmount, req.ExpectedCurrency, variables)
	return captureFromChat(ctx, chat, req, template)
}

func renderPaymentRequest(template, amount, currency string, variables map[string]string) string {
	return tradechat.RenderPaymentRequestWithVariables(template, amount, currency, variables)
}

// TradeChatFor resolves the venue chat transport (used by dexnotify relay registration).
func (p *PaymentResolver) TradeChatFor(ctx context.Context, exchange string) (TradeChat, error) {
	return p.tradeChat(ctx, exchange)
}

func captureFromChat(ctx context.Context, chat TradeChat, req ChatRequest, template string) (json.RawMessage, error) {
	result, err := tradechat.EnsurePaymentRequest(ctx, chat, tradechat.PaymentRequest{
		OrderID:          req.OrderID,
		ExpectedAmount:   req.ExpectedAmount,
		ExpectedCurrency: req.ExpectedCurrency,
	}, template)
	if err != nil {
		return nil, err
	}
	return marshalChatEnvelope(req, result.Messages)
}

func (p *PaymentResolver) tradeChat(ctx context.Context, exchange string) (TradeChat, error) {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexMostro:
		if p.mostro == nil {
			return nil, fmt.Errorf("mostro chat is not configured")
		}
		return p.mostro, nil
	case db.DexRoboSats:
		if p.robosats == nil && p.robosatsDial == nil {
			return nil, fmt.Errorf("robosats client is not configured")
		}
		return robosatsChat{fallback: p.roboSatsClient(ctx), dial: p.robosatsDial, repo: p.repo, coordinator: p.roboCoordinator}, nil
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
	case db.DexBisq:
		return nil, fmt.Errorf("%w: Bisq1 daemon API exposes no trade-chat transport", tradechat.ErrTradeChatUnavailable)
	default:
		return nil, fmt.Errorf("dexaccount: chat flow not implemented for exchange %q", exchange)
	}
}

type robosatsChat struct {
	coordinator *robosats.Coordinator
	fallback    *robosats.Client
	dial        func(coordinatorID, token string) (*robosats.Client, error)
	repo        *db.TransactionRepository
}

func (c robosatsChat) clientFor(ctx context.Context, orderRef string) (*robosats.Client, string, error) {
	coordinatorID, orderID := robosats.SplitTradeRef(orderRef)
	if orderID == "" {
		return nil, "", fmt.Errorf("robosats: order ID is required")
	}
	if c.coordinator != nil && c.repo != nil {
		s, err := c.repo.RoboSatsSessionForRef(ctx, coordinatorID, orderID)
		if err != nil {
			return nil, "", err
		}
		api, err := c.coordinator.SessionAPI(ctx, s)
		if err != nil {
			return nil, "", err
		}
		client, ok := api.(*robosats.Client)
		if !ok {
			return nil, "", fmt.Errorf("RoboSats pinned chat client unavailable")
		}
		return client, orderID, nil
	}
	if coordinatorID != "" && c.dial != nil {
		token := robosatsTokenForRepo(ctx, c.repo)
		client, err := c.dial(coordinatorID, token)
		if err != nil {
			return nil, "", err
		}
		if client != nil {
			return client, orderID, nil
		}
	}
	if c.fallback == nil {
		return nil, "", fmt.Errorf("robosats client is not configured")
	}
	return c.fallback, orderID, nil
}

func (c robosatsChat) SendMessage(ctx context.Context, orderID, text string) error {
	client, nativeID, err := c.clientFor(ctx, orderID)
	if err != nil {
		return err
	}
	return client.SendChatMessage(ctx, nativeID, text)
}

// EnsurePaymentRequest bypasses marker-only deduplication: only a message
// authenticated by the admitted robot's PGP key can acknowledge this template.
func (c robosatsChat) EnsurePaymentRequest(ctx context.Context, ref, text string) error {
	if c.coordinator == nil || c.repo == nil {
		return fmt.Errorf("RoboSats durable chat is unavailable")
	}
	coordinator, order := robosats.SplitTradeRef(ref)
	s, err := c.repo.RoboSatsSessionForRef(ctx, coordinator, order)
	if err != nil {
		return err
	}
	return c.coordinator.ReconcileChat(ctx, s.Request.RequestID, text, s.RobotNick)
}

func (c robosatsChat) Messages(ctx context.Context, orderID string) ([]TradeChatMessage, error) {
	client, nativeID, err := c.clientFor(ctx, orderID)
	if err != nil {
		return nil, err
	}
	thread, err := client.GetChatMessages(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	out := make([]TradeChatMessage, 0, len(thread.Messages))
	for _, m := range thread.Messages {
		out = append(out, TradeChatMessage{Time: m.Time, Sender: m.Nick, Text: m.Plaintext, Outbound: m.Own})
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

func isInbound(m TradeChatMessage) bool {
	return !m.Outbound && !strings.Contains(m.Text, paymentRequestMarker)
}

func (p *PaymentResolver) roboSatsClient(ctx context.Context) *robosats.Client {
	if token := robosatsTokenForRepo(ctx, p.repo); token != "" {
		return p.robosats.WithAuthToken(token)
	}
	return p.robosats
}
