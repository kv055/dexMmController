// Package tradechat coordinates venue trade chats and the shared Telegram relay.
package tradechat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"commons/telegram"
	"dexMmController/internal/db"
)

// PaymentRequestMarker is included in every automated payment-details request.
// It is deliberately stable so custom templates are idempotent too.
const PaymentRequestMarker = telegram.AutomatedPaymentRequestMarker

var (
	// ErrTradeChatUnavailable means the venue has no configured chat transport.
	ErrTradeChatUnavailable = errors.New("trade chat unavailable")
	// ErrUnsupportedExchange means the package has no chat policy for the exchange.
	ErrUnsupportedExchange = errors.New("unsupported trade-chat exchange")
)

// TradeChatFactory resolves the chat transport for a venue.
type TradeChatFactory interface {
	TradeChatFor(ctx context.Context, exchange string) (telegram.TradeChat, error)
}

// TemplateStore loads a payment-details template for a payment method.
type TemplateStore interface {
	GetChatTemplate(ctx context.Context, exchange, paymentMethodName string) (string, error)
}

// RelayRegistrar registers a venue chat with the shared Telegram relay.
type RelayRegistrar interface {
	Register(exchange, orderID string, chat telegram.TradeChat, knownOutbound ...string)
}

// Client coordinates payment-request chats and relay-only trade chats.
type Client struct {
	factory   TradeChatFactory
	templates TemplateStore
	relay     RelayRegistrar

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// New creates a reusable trade-chat client. The template store is required only
// for venues that need an automated payment-details request.
func New(factory TradeChatFactory, templates TemplateStore, relay RelayRegistrar) *Client {
	return &Client{factory: factory, templates: templates, relay: relay, locks: make(map[string]*sync.Mutex)}
}

// AfterFunded registers a funded trade chat with Telegram. RoboSats and Bisq2
// also receive a single payment-details request; HodlHodl is relay-only.
func (c *Client) AfterFunded(ctx context.Context, tx *db.Transaction) error {
	if c == nil || c.factory == nil || c.relay == nil {
		return errors.New("tradechat: client is not fully configured")
	}
	if tx == nil {
		return errors.New("tradechat: transaction is required")
	}
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return fmt.Errorf("tradechat: parse offer metadata: %w", err)
	}
	orderID := strings.TrimSpace(meta.ContractID())
	if orderID == "" {
		return errors.New("tradechat: order ID is required")
	}
	exchange := strings.TrimSpace(meta.Exchange())
	if exchange == "" {
		exchange = strings.TrimSpace(string(tx.Dex))
	}
	if exchange == "" {
		return errors.New("tradechat: exchange is required")
	}
	if !requiresPaymentRequest(exchange) && !relayOnlyVenue(exchange) && db.DexPlatformFromSlug(exchange) != db.DexBisq {
		return fmt.Errorf("tradechat: %s: %w", exchange, ErrUnsupportedExchange)
	}

	lock := c.lockFor(exchange, orderID)
	lock.Lock()
	defer lock.Unlock()

	chat, err := c.factory.TradeChatFor(ctx, exchange)
	if err != nil {
		return fmt.Errorf("tradechat: resolve %s chat: %w", exchange, err)
	}
	if chat == nil {
		return fmt.Errorf("tradechat: resolve %s chat: %w", exchange, ErrTradeChatUnavailable)
	}

	var knownOutbound []string
	if requiresPaymentRequest(exchange) {
		if c.templates == nil {
			return errors.New("tradechat: template store is required for payment-request venue")
		}
		template, err := c.templates.GetChatTemplate(ctx, exchange, tx.PaymentMethod)
		if err != nil {
			return fmt.Errorf("tradechat: load chat template: %w", err)
		}
		result, err := EnsurePaymentRequest(ctx, chat, PaymentRequest{
			OrderID:          orderID,
			ExpectedAmount:   strconv.FormatFloat(tx.FiatAmount, 'f', 2, 64),
			ExpectedCurrency: tx.FiatCurrency,
		}, template)
		if err != nil {
			return err
		}
		knownOutbound = result.KnownOutbound
	} else if !relayOnlyVenue(exchange) {
		return fmt.Errorf("tradechat: %s: %w", exchange, ErrUnsupportedExchange)
	}

	c.relay.Register(exchange, orderID, chat, knownOutbound...)
	return nil
}

func (c *Client) lockFor(exchange, orderID string) *sync.Mutex {
	key := strings.ToLower(strings.TrimSpace(exchange)) + ":" + strings.TrimSpace(orderID)
	c.locksMu.Lock()
	defer c.locksMu.Unlock()
	if lock := c.locks[key]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	c.locks[key] = lock
	return lock
}

// PaymentRequest contains the data needed to render one automated request.
type PaymentRequest struct {
	OrderID          string
	ExpectedAmount   string
	ExpectedCurrency string
}

// RequestResult exposes the messages used by legacy counterparty capture and
// the outbound texts that must not be echoed by the Telegram relay.
type RequestResult struct {
	Messages      []telegram.Message
	KnownOutbound []string
}

const defaultPaymentRequestTemplate = `Hi! To release the Bitcoin I need to confirm your payment.

Please reply here with:
• The full name on your account
• The account / handle / IBAN the payment will come from
• The payment method

I will release the Bitcoin only after I receive exactly {{amount}} {{currency}} and the sender name, handle and payment details match what you provide in this chat. If they don't match, I'll have to open a dispute. Thank you!`

// RenderPaymentRequest renders a custom or default request and adds its marker.
func RenderPaymentRequest(template, amount, currency string) string {
	if strings.TrimSpace(template) == "" {
		template = defaultPaymentRequestTemplate
	}
	amount = strings.TrimSpace(amount)
	currency = strings.TrimSpace(currency)
	if amount == "" && currency == "" {
		amount = "the agreed amount"
	}
	out := strings.ReplaceAll(template, "{{amount}}", amount)
	out = strings.ReplaceAll(out, "{{currency}}", currency)
	out = strings.ReplaceAll(out, "  ", " ")
	if !strings.Contains(out, PaymentRequestMarker) {
		out = strings.TrimSpace(out) + "\n\n" + PaymentRequestMarker
	}
	return out
}

// EnsurePaymentRequest sends the marked request once and returns the current
// thread for callers that also capture counterparty details.
func EnsurePaymentRequest(ctx context.Context, chat telegram.TradeChat, req PaymentRequest, template string) (RequestResult, error) {
	if chat == nil {
		return RequestResult{}, errors.New("tradechat: chat is required")
	}
	if strings.TrimSpace(req.OrderID) == "" {
		return RequestResult{}, errors.New("tradechat: order ID is required")
	}
	msgs, err := chat.Messages(ctx, req.OrderID)
	if err != nil {
		return RequestResult{}, fmt.Errorf("tradechat: read messages: %w", err)
	}
	known := markedMessages(msgs)
	if len(known) == 0 {
		outbound := RenderPaymentRequest(template, req.ExpectedAmount, req.ExpectedCurrency)
		if err := chat.SendMessage(ctx, req.OrderID, outbound); err != nil {
			return RequestResult{}, fmt.Errorf("tradechat: send payment request: %w", err)
		}
		known = append(known, outbound)
		if refreshed, rerr := chat.Messages(ctx, req.OrderID); rerr == nil {
			msgs = refreshed
		}
	}
	return RequestResult{Messages: msgs, KnownOutbound: uniqueTexts(known)}, nil
}

func markedMessages(msgs []telegram.Message) []string {
	out := make([]string, 0, 1)
	for _, msg := range msgs {
		if strings.Contains(msg.Text, PaymentRequestMarker) {
			out = append(out, msg.Text)
		}
	}
	return out
}

func uniqueTexts(texts []string) []string {
	seen := make(map[string]struct{}, len(texts))
	out := make([]string, 0, len(texts))
	for _, text := range texts {
		key := strings.TrimSpace(text)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, text)
	}
	return out
}

func requiresPaymentRequest(exchange string) bool {
	switch db.DexPlatformFromSlug(exchange) {
	case db.DexRoboSats, db.DexBisq2:
		return true
	default:
		return false
	}
}

func relayOnlyVenue(exchange string) bool {
	return db.DexPlatformFromSlug(exchange) == db.DexHodlHodl
}
