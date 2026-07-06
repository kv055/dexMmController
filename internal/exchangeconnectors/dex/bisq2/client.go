package bisq2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"commons/telegram"
)

const (
	defaultRESTPrefix = "/api/v1"
	defaultTimeout    = 30 * time.Second
)

// Client talks to the Bisq2 Easy API daemon (REST + WebSocket on port 8090).
type Client struct {
	baseURL    string
	token      string
	restPrefix string
	http       *http.Client

	mu             sync.RWMutex
	channelByTrade map[string]string
	messages       map[string][]telegram.Message
	wsCancel       context.CancelFunc
}

// NewClient creates a Bisq2 client. baseURL is host:port or http://host:port (no trailing slash).
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	baseURL = normalizeBaseURL(baseURL)
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:        baseURL,
		token:          strings.TrimSpace(token),
		restPrefix:     defaultRESTPrefix,
		http:           httpClient,
		channelByTrade: make(map[string]string),
		messages:       make(map[string][]telegram.Message),
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
	return strings.Replace(c.baseURL, "https://", "wss://", 1) + "/websocket"
}

func (c *Client) authorized(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	if c.baseURL == "" {
		return errors.New("bisq2: base URL is not configured")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(path), bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.authorized(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bisq2: POST %s: status %d", path, resp.StatusCode)
	}
	return nil
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
