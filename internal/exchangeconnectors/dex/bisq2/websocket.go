package bisq2

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"commons/telegram"
	"nhooyr.io/websocket"
)

type wsEnvelope struct {
	Type string `json:"type"`
}

type subscriptionRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Topic     string `json:"topic"`
}

type subscriptionResponse struct {
	Type         string  `json:"type"`
	RequestID    string  `json:"requestId"`
	Payload      *string `json:"payload"`
	ErrorMessage *string `json:"errorMessage"`
}

type webSocketEvent struct {
	Type    string `json:"type"`
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
}

// openTradeMessageDTO mirrors bisq.api.dto.chat.bisq_easy.open_trades.BisqEasyOpenTradeMessageDto.
type openTradeMessageDTO struct {
	TradeID   string `json:"tradeId"`
	ChannelID string `json:"channelId"`
	Sender    struct {
		UserName string `json:"userName"`
	} `json:"senderUserProfile"`
	Text json.RawMessage `json:"text"`
	Date int64           `json:"date"`
}

func (c *Client) runWebSocket(ctx context.Context) {
	backoff := 3 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.connectOnce(ctx); err != nil {
			log.Printf("bisq2: websocket: %v; retry in %s", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (c *Client) connectOnce(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, c.wsURL(), nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "shutdown")

	sub := subscriptionRequest{
		Type:      "SubscriptionRequest",
		RequestID: "trade-chat-1",
		Topic:     "TRADE_CHAT_MESSAGES",
	}
	b, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return err
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		c.ingestWSMessage(data)
	}
}

func (c *Client) ingestWSMessage(raw []byte) {
	var env wsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	switch env.Type {
	case "SubscriptionResponse":
		var resp subscriptionResponse
		if err := json.Unmarshal(raw, &resp); err != nil || resp.Payload == nil {
			return
		}
		c.ingestPayload(*resp.Payload, false)
	case "WebSocketEvent":
		var ev webSocketEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return
		}
		if ev.Topic != "TRADE_CHAT_MESSAGES" || ev.Payload == "" {
			return
		}
		c.ingestPayload(ev.Payload, true)
	}
}

func (c *Client) ingestPayload(payload string, single bool) {
	if single {
		var msg openTradeMessageDTO
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return
		}
		c.storeMessage(msg)
		return
	}
	var batch []openTradeMessageDTO
	if err := json.Unmarshal([]byte(payload), &batch); err != nil {
		var one openTradeMessageDTO
		if err2 := json.Unmarshal([]byte(payload), &one); err2 == nil {
			c.storeMessage(one)
		}
		return
	}
	for _, msg := range batch {
		c.storeMessage(msg)
	}
}

func (c *Client) storeMessage(dto openTradeMessageDTO) {
	text := decodeOptionalText(dto.Text)
	if strings.TrimSpace(dto.TradeID) == "" {
		return
	}
	ts := time.UnixMilli(dto.Date).UTC().Format(time.RFC3339)
	msg := telegram.Message{
		Time:   ts,
		Sender: strings.TrimSpace(dto.Sender.UserName),
		Text:   text,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if dto.ChannelID != "" {
		c.channelByTrade[dto.TradeID] = dto.ChannelID
	}
	c.messages[dto.TradeID] = appendUniqueMessage(c.messages[dto.TradeID], msg)
}

func appendUniqueMessage(existing []telegram.Message, m telegram.Message) []telegram.Message {
	fp := strings.TrimSpace(m.Time) + "|" + strings.TrimSpace(m.Sender) + "|" + strings.TrimSpace(m.Text)
	for _, e := range existing {
		if strings.TrimSpace(e.Time)+"|"+strings.TrimSpace(e.Sender)+"|"+strings.TrimSpace(e.Text) == fp {
			return existing
		}
	}
	return append(existing, m)
}

func decodeOptionalText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// Ping checks that the Bisq2 REST API is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Version(ctx)
	return err
}

// Version returns the Bisq2 Easy API release reported by its settings endpoint.
func (c *Client) Version(ctx context.Context) (string, error) {
	if c.baseURL == "" {
		return "", fmt.Errorf("bisq2: base URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("/settings/version"), nil)
	if err != nil {
		return "", err
	}
	c.authorized(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("bisq2: version probe: status %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("bisq2: decode version: %w", err)
	}
	if strings.TrimSpace(body.Version) == "" {
		return "", fmt.Errorf("bisq2: version response is empty")
	}
	return body.Version, nil
}
