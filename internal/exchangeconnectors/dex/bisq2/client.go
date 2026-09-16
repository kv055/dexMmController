package bisq2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"commons/telegram"
)

const (
	defaultRESTPrefix   = "/api/v1"
	defaultTimeout      = 30 * time.Second
	takeOfferTimeout    = 160 * time.Second
	maxErrorBodyBytes   = 4096
	quoteAmountScale    = 10000
	depositPollInterval = 3 * time.Second
	// DepositPollTimeout is how long ResolveDeposit waits for the buyer to send
	// bitcoinPaymentData after take. Mailbox delivery can lag when the maker is offline.
	DepositPollTimeout = 10 * time.Minute
)

// Client talks to the Bisq2 Easy API daemon (REST + WebSocket on port 8090).
type Client struct {
	baseURL    string
	token      string
	restPrefix string
	http       *http.Client

	mu                 sync.RWMutex
	channelByTrade     map[string]string
	messages           map[string][]telegram.Message
	bitcoinPaymentData map[string]string
	paymentAccountData map[string]string
	tradeState         map[string]string
	wsCancel           context.CancelFunc
}

// NewClient creates a Bisq2 client. baseURL is host:port or http://host:port (no trailing slash).
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	baseURL = normalizeBaseURL(baseURL)
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:            baseURL,
		token:              strings.TrimSpace(token),
		restPrefix:         defaultRESTPrefix,
		http:               httpClient,
		channelByTrade:     make(map[string]string),
		messages:           make(map[string][]telegram.Message),
		bitcoinPaymentData: make(map[string]string),
		paymentAccountData: make(map[string]string),
		tradeState:         make(map[string]string),
	}
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

func (c *Client) apiURL(path string) string {
	return c.baseURL + c.restPrefix + path
}

func (c *Client) wsURL() string {
	u := c.baseURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/websocket"
}

func (c *Client) authorized(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, timeout time.Duration) ([]byte, int, error) {
	if c.baseURL == "" {
		return nil, 0, errors.New("bisq2: base URL is not configured")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), rdr)
	if err != nil {
		return nil, 0, err
	}
	c.authorized(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.http
	if timeout > 0 && (client.Timeout == 0 || timeout > client.Timeout) {
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(payload))
		if len(msg) > maxErrorBodyBytes {
			msg = msg[:maxErrorBodyBytes]
		}
		if msg == "" {
			msg = resp.Status
		}
		return payload, resp.StatusCode, fmt.Errorf("bisq2: %s %s: status %d: %s", method, path, resp.StatusCode, msg)
	}
	return payload, resp.StatusCode, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	_, _, err := c.doJSON(ctx, http.MethodPost, path, body, 0)
	return err
}

// Offer describes the rails advertised on a Bisq Easy offerbook item.
type Offer struct {
	ID                   string
	FiatRails            []string
	BitcoinRails         []string
	QuoteSidePaymentSpec []string
}

// GetOffer loads one offer from the currency offerbook. The daemon has no
// GET-by-id; take also looks the offer up from the in-memory book.
func (c *Client) GetOffer(ctx context.Context, currency, offerID string) (*Offer, error) {
	offerID = strings.TrimSpace(offerID)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if offerID == "" {
		return nil, errors.New("bisq2: offer ID is required")
	}
	if currency == "" {
		return nil, errors.New("bisq2: fiat currency is required to look up an offer")
	}
	path := "/offerbook/markets/" + url.PathEscape(currency) + "/offers"
	payload, _, err := c.doJSON(ctx, http.MethodGet, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var items []offerbookItem
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, fmt.Errorf("bisq2: decode offerbook: %w", err)
	}
	for _, item := range items {
		got := item.BisqEasyOffer.ID
		if got == "" {
			got = item.ID
		}
		if got != offerID {
			continue
		}
		return item.asOffer(), nil
	}
	return nil, fmt.Errorf("bisq2: offer %q not found in %s offerbook", offerID, currency)
}

type offerbookItem struct {
	ID                      string   `json:"id"`
	QuoteSidePaymentMethods []string `json:"quoteSidePaymentMethods"`
	BaseSidePaymentMethods  []string `json:"baseSidePaymentMethods"`
	BisqEasyOffer           struct {
		ID                          string `json:"id"`
		QuoteSidePaymentMethodSpecs []struct {
			PaymentMethod string `json:"paymentMethod"`
		} `json:"quoteSidePaymentMethodSpecs"`
		BaseSidePaymentMethodSpecs []struct {
			PaymentMethod string `json:"paymentMethod"`
		} `json:"baseSidePaymentMethodSpecs"`
	} `json:"bisqEasyOffer"`
}

func (item offerbookItem) asOffer() *Offer {
	o := &Offer{ID: item.BisqEasyOffer.ID}
	if o.ID == "" {
		o.ID = item.ID
	}
	o.FiatRails = append([]string(nil), item.QuoteSidePaymentMethods...)
	if len(o.FiatRails) == 0 {
		for _, spec := range item.BisqEasyOffer.QuoteSidePaymentMethodSpecs {
			if m := strings.TrimSpace(spec.PaymentMethod); m != "" {
				o.FiatRails = append(o.FiatRails, m)
			}
		}
	}
	o.BitcoinRails = append([]string(nil), item.BaseSidePaymentMethods...)
	if len(o.BitcoinRails) == 0 {
		for _, spec := range item.BisqEasyOffer.BaseSidePaymentMethodSpecs {
			if m := strings.TrimSpace(spec.PaymentMethod); m != "" {
				o.BitcoinRails = append(o.BitcoinRails, m)
			}
		}
	}
	o.QuoteSidePaymentSpec = append([]string(nil), o.FiatRails...)
	return o
}

// TakeOfferParams is the amount and rail selection for POST /trades.
type TakeOfferParams struct {
	BaseSideAmount       int64
	QuoteSideAmount      int64
	BitcoinPaymentMethod string
	FiatPaymentMethod    string
}

// TakeResult is the daemon's take-offer response.
type TakeResult struct {
	TradeID              string
	BitcoinPaymentMethod string
	FiatPaymentMethod    string
}

// QuoteAmountUnits converts a decimal fiat amount into Bisq Easy's 4-decimal integer units.
func QuoteAmountUnits(fiat float64) int64 {
	if fiat <= 0 {
		return 0
	}
	return int64(fiat*quoteAmountScale + 0.5)
}

// TakeOffer creates a Bisq Easy trade by taking an offer (POST /api/v1/trades).
func (c *Client) TakeOffer(ctx context.Context, offerID string, params TakeOfferParams) (*TakeResult, error) {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return nil, errors.New("bisq2: offer ID is required")
	}
	if params.QuoteSideAmount <= 0 {
		return nil, errors.New("bisq2: quote-side amount is required")
	}
	if params.BaseSideAmount <= 0 {
		return nil, errors.New("bisq2: base-side amount is required")
	}
	bitcoinMethod := strings.TrimSpace(params.BitcoinPaymentMethod)
	if bitcoinMethod == "" {
		bitcoinMethod = BitcoinRailMainChain
	}
	fiatMethod := strings.TrimSpace(params.FiatPaymentMethod)
	if fiatMethod == "" {
		return nil, errors.New("bisq2: fiat payment method is required")
	}
	body := map[string]any{
		"offerId":              offerID,
		"baseSideAmount":       params.BaseSideAmount,
		"quoteSideAmount":      params.QuoteSideAmount,
		"bitcoinPaymentMethod": bitcoinMethod,
		"fiatPaymentMethod":    fiatMethod,
	}
	payload, _, err := c.doJSON(ctx, http.MethodPost, "/trades", body, takeOfferTimeout)
	if err != nil {
		return nil, err
	}
	var resp struct {
		TradeID string `json:"tradeId"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("bisq2: decode take response: %w", err)
	}
	if strings.TrimSpace(resp.TradeID) == "" {
		return nil, errors.New("bisq2: take offer returned no trade id")
	}
	return &TakeResult{
		TradeID:              resp.TradeID,
		BitcoinPaymentMethod: bitcoinMethod,
		FiatPaymentMethod:    fiatMethod,
	}, nil
}

// TradeEventType is a Bisq Easy PATCH /trades/{id}/event discriminator.
type TradeEventType string

const (
	EventSellerSendsPaymentAccount TradeEventType = "SELLER_SENDS_PAYMENT_ACCOUNT"
	EventSellerConfirmFiatReceipt  TradeEventType = "SELLER_CONFIRM_FIAT_RECEIPT"
	EventSellerConfirmBtcSent      TradeEventType = "SELLER_CONFIRM_BTC_SENT"
	EventBtcConfirmed              TradeEventType = "BTC_CONFIRMED"
)

// ProcessTradeEvent sends a protocol event for an open trade.
func (c *Client) ProcessTradeEvent(ctx context.Context, tradeID string, event TradeEventType, data string) error {
	tradeID = strings.TrimSpace(tradeID)
	if tradeID == "" {
		return errors.New("bisq2: trade ID is required")
	}
	if event == "" {
		return errors.New("bisq2: trade event type is required")
	}
	body := map[string]any{"tradeEventType": string(event)}
	if strings.TrimSpace(data) != "" {
		body["data"] = data
	}
	_, _, err := c.doJSON(ctx, http.MethodPatch, "/trades/"+url.PathEscape(tradeID)+"/event", body, 0)
	return err
}

// SendSellerPaymentAccount publishes our fiat details on the seller protocol path.
func (c *Client) SendSellerPaymentAccount(ctx context.Context, tradeID, paymentAccountData string) error {
	if strings.TrimSpace(paymentAccountData) == "" {
		return errors.New("bisq2: payment account data is required")
	}
	return c.ProcessTradeEvent(ctx, tradeID, EventSellerSendsPaymentAccount, paymentAccountData)
}

// ConfirmBtcSent tells the daemon the seller sent BTC. On-chain trades require a txid.
func (c *Client) ConfirmBtcSent(ctx context.Context, tradeID, paymentProof string) error {
	return c.ProcessTradeEvent(ctx, tradeID, EventSellerConfirmBtcSent, paymentProof)
}

// ConfirmFiatReceipt tells the daemon the seller received the fiat payment.
func (c *Client) ConfirmFiatReceipt(ctx context.Context, tradeID string) error {
	return c.ProcessTradeEvent(ctx, tradeID, EventSellerConfirmFiatReceipt, "")
}

// BitcoinPaymentData returns the cached buyer destination for a trade, if any.
func (c *Client) BitcoinPaymentData(tradeID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.bitcoinPaymentData[tradeID])
}

// WaitForBitcoinPaymentData blocks until the buyer has sent bitcoinPaymentData
// (address or invoice) over the Bisq Easy protocol.
func (c *Client) WaitForBitcoinPaymentData(ctx context.Context, tradeID string, timeout time.Duration) (string, error) {
	tradeID = strings.TrimSpace(tradeID)
	if tradeID == "" {
		return "", errors.New("bisq2: trade ID is required")
	}
	if timeout <= 0 {
		timeout = DepositPollTimeout
	}
	if data := c.BitcoinPaymentData(tradeID); data != "" {
		return data, nil
	}
	if err := c.ensureWebSocket(ctx); err != nil {
		return "", err
	}
	if data := c.BitcoinPaymentData(tradeID); data != "" {
		return data, nil
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(depositPollInterval)
	defer ticker.Stop()
	for {
		if remaining := time.Until(deadline); remaining <= 0 {
			return "", fmt.Errorf("bisq2: bitcoin payment data not received for trade %q before timeout", tradeID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if data := c.BitcoinPaymentData(tradeID); data != "" {
				return data, nil
			}
		}
	}
}

// SendChatMessage posts plaintext to a trade channel. orderID is the Bisq2 trade id;
// channelId is resolved from the websocket cache when available.
func (c *Client) SendChatMessage(ctx context.Context, tradeID, plaintext string) error {
	if tradeID == "" {
		return errors.New("bisq2: trade ID is required")
	}
	channelID, err := c.resolveChannelID(ctx, tradeID)
	if err != nil {
		return err
	}
	body := map[string]any{"text": plaintext}
	return c.postJSON(ctx, "/trade-chat-channels/"+channelID+"/messages", body)
}

// GetChatMessages returns cached trade-chat messages for a trade id (websocket-backed).
func (c *Client) GetChatMessages(ctx context.Context, tradeID string) ([]telegram.Message, error) {
	if tradeID == "" {
		return nil, errors.New("bisq2: trade ID is required")
	}
	if err := c.ensureWebSocket(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := append([]telegram.Message(nil), c.messages[tradeID]...)
	return out, nil
}

func (c *Client) resolveChannelID(ctx context.Context, tradeID string) (string, error) {
	c.mu.RLock()
	channelID, ok := c.channelByTrade[tradeID]
	c.mu.RUnlock()
	if ok && channelID != "" {
		return channelID, nil
	}
	if err := c.ensureWebSocket(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	channelID, ok = c.channelByTrade[tradeID]
	c.mu.RUnlock()
	if !ok || channelID == "" {
		return "", fmt.Errorf("bisq2: channel id not yet known for trade %q (waiting for chat sync)", tradeID)
	}
	return channelID, nil
}

func (c *Client) ensureWebSocket(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wsCancel != nil {
		return nil
	}
	if c.baseURL == "" {
		return errors.New("bisq2: base URL is not configured")
	}
	wsCtx, cancel := context.WithCancel(context.Background())
	c.wsCancel = cancel
	go c.runWebSocket(wsCtx)
	return nil
}

func (c *Client) storeBitcoinPaymentData(tradeID, data string) {
	tradeID = strings.TrimSpace(tradeID)
	data = strings.TrimSpace(data)
	if tradeID == "" || data == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bitcoinPaymentData[tradeID] = data
}

func (c *Client) storePaymentAccountData(tradeID, data string) {
	tradeID = strings.TrimSpace(tradeID)
	data = strings.TrimSpace(data)
	if tradeID == "" || data == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paymentAccountData[tradeID] = data
}

func (c *Client) storeTradeState(tradeID, state string) {
	tradeID = strings.TrimSpace(tradeID)
	state = strings.TrimSpace(state)
	if tradeID == "" || state == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tradeState[tradeID] = state
}

func (c *Client) storeChannel(tradeID, channelID string) {
	tradeID = strings.TrimSpace(tradeID)
	channelID = strings.TrimSpace(channelID)
	if tradeID == "" || channelID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channelByTrade[tradeID] = channelID
}

// Close stops the background websocket reader.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wsCancel != nil {
		c.wsCancel()
		c.wsCancel = nil
	}
}

// TradeChat adapts Client to commons/telegram.TradeChat. orderID is the Bisq2 trade id.
type TradeChat struct {
	Client *Client
}

func (t TradeChat) SendMessage(ctx context.Context, orderID, text string) error {
	if t.Client == nil {
		return errors.New("bisq2: client is not configured")
	}
	return t.Client.SendChatMessage(ctx, orderID, text)
}

func (t TradeChat) Messages(ctx context.Context, orderID string) ([]telegram.Message, error) {
	if t.Client == nil {
		return nil, errors.New("bisq2: client is not configured")
	}
	return t.Client.GetChatMessages(ctx, orderID)
}
