// Package magma implements Amboss Magma's BLIP-0051-compatible LSP REST client.
package magma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://magma.amboss.tech/api/lsp/v1"

// Pool selects which Magma seller pool fulfils an order.
type Pool string

const (
	PoolRecommended Pool = "recommended"
	PoolCheapest    Pool = "cheapest"
)

func (p Pool) Validate() error {
	switch p {
	case PoolRecommended, PoolCheapest:
		return nil
	default:
		return fmt.Errorf("magma pool must be %q or %q", PoolRecommended, PoolCheapest)
	}
}

// Info is the BLIP-0051 get_info response.
type Info struct {
	MinRequiredChannelConfirmations int      `json:"min_required_channel_confirmations"`
	MinFundingConfirmsWithinBlocks  int      `json:"min_funding_confirms_within_blocks"`
	SupportsZeroChannelReserve        bool     `json:"supports_zero_channel_reserve"`
	MaxChannelExpiryBlocks            int      `json:"max_channel_expiry_blocks"`
	MinInitialClientBalanceSat        string   `json:"min_initial_client_balance_sat"`
	MaxInitialClientBalanceSat        string   `json:"max_initial_client_balance_sat"`
	MinInitialLSPBalanceSat           string   `json:"min_initial_lsp_balance_sat"`
	MaxInitialLSPBalanceSat           string   `json:"max_initial_lsp_balance_sat"`
	MinChannelBalanceSat              string   `json:"min_channel_balance_sat"`
	MaxChannelBalanceSat              string   `json:"max_channel_balance_sat"`
	URIs                              []string `json:"uris"`
}

// CreateOrderRequest is the BLIP-0051 create_order body.
type CreateOrderRequest struct {
	LSPBalanceSat                  string `json:"lsp_balance_sat"`
	ClientBalanceSat               string `json:"client_balance_sat"`
	RequiredChannelConfirmations   int    `json:"required_channel_confirmations"`
	FundingConfirmsWithinBlocks    int    `json:"funding_confirms_within_blocks"`
	ChannelExpiryBlocks            int    `json:"channel_expiry_blocks"`
	Token                          string `json:"token"`
	RefundOnchainAddress           string `json:"refund_onchain_address,omitempty"`
	AnnounceChannel                bool   `json:"announce_channel"`
	PublicKey                      string `json:"public_key"`
}

// PaymentBolt11 holds invoice payment details from Magma.
type PaymentBolt11 struct {
	State         string `json:"state"`
	ExpiresAt     string `json:"expires_at"`
	FeeTotalSat   string `json:"fee_total_sat"`
	OrderTotalSat string `json:"order_total_sat"`
	Invoice       string `json:"invoice"`
}

// Payment wraps the payment section of an order response.
type Payment struct {
	Bolt11 PaymentBolt11 `json:"bolt11"`
}

// Channel describes a funded LSP channel once the order completes.
type Channel struct {
	FundedAt        string `json:"funded_at"`
	FundingOutpoint string `json:"funding_outpoint"`
	ExpiresAt       string `json:"expires_at"`
}

// Order is the shared Magma order response shape.
type Order struct {
	OrderID                        string   `json:"order_id"`
	LSPBalanceSat                  string   `json:"lsp_balance_sat"`
	ClientBalanceSat               string   `json:"client_balance_sat"`
	RequiredChannelConfirmations   int      `json:"required_channel_confirmations"`
	FundingConfirmsWithinBlocks    int      `json:"funding_confirms_within_blocks"`
	ChannelExpiryBlocks            int      `json:"channel_expiry_blocks"`
	Token                          string   `json:"token"`
	CreatedAt                      string   `json:"created_at"`
	AnnounceChannel                bool     `json:"announce_channel"`
	OrderState                     string   `json:"order_state"`
	Payment                        Payment  `json:"payment"`
	Channel                        *Channel `json:"channel"`
}

// APIError is Magma's Nest-style error body.
type APIError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

func (e APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("magma API error (status %d)", e.StatusCode)
}

// Client calls the Magma BLIP-0051 REST endpoints.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a client with the default Magma base URL when baseURL is empty.
func NewClient(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	return &Client{BaseURL: baseURL, HTTPClient: httpClient}
}

func (c *Client) poolURL(pool Pool, suffix string) (string, error) {
	if err := pool.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/%s", c.BaseURL, pool, strings.TrimPrefix(suffix, "/")), nil
}

// GetInfo returns LSP capabilities for the selected pool.
func (c *Client) GetInfo(ctx context.Context, pool Pool) (Info, error) {
	endpoint, err := c.poolURL(pool, "get_info")
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}

// CreateOrder reserves inbound capacity and returns a BOLT11 invoice.
func (c *Client) CreateOrder(ctx context.Context, pool Pool, req CreateOrderRequest) (Order, error) {
	endpoint, err := c.poolURL(pool, "create_order")
	if err != nil {
		return Order{}, err
	}
	var order Order
	if err := c.do(ctx, http.MethodPost, endpoint, req, &order); err != nil {
		return Order{}, err
	}
	return order, nil
}

// GetOrder polls the current state of an existing order.
func (c *Client) GetOrder(ctx context.Context, pool Pool, orderID string) (Order, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return Order{}, fmt.Errorf("order_id is required")
	}
	base, err := c.poolURL(pool, "get_order")
	if err != nil {
		return Order{}, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return Order{}, fmt.Errorf("parse get_order URL: %w", err)
	}
	q := u.Query()
	q.Set("order_id", orderID)
	u.RawQuery = q.Encode()

	var order Order
	if err := c.do(ctx, http.MethodGet, u.String(), nil, &order); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("magma client is nil")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode magma request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("magma request failed: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read magma response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr APIError
		if json.Unmarshal(payload, &apiErr) == nil && (apiErr.Message != "" || apiErr.StatusCode != 0) {
			if apiErr.StatusCode == 0 {
				apiErr.StatusCode = resp.StatusCode
			}
			return apiErr
		}
		return fmt.Errorf("magma HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode magma response: %w", err)
	}
	return nil
}

// ParseSatString parses Magma's string-encoded satoshi amounts.
func ParseSatString(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse sat amount %q: %w", raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("sat amount cannot be negative")
	}
	return n, nil
}

// ParseURI splits a BLIP pubkey@host:port connection string.
func ParseURI(uri string) (pubkey, host string, err error) {
	uri = strings.TrimSpace(uri)
	at := strings.LastIndex(uri, "@")
	if at <= 0 || at >= len(uri)-1 {
		return "", "", fmt.Errorf("invalid magma URI %q", uri)
	}
	return uri[:at], uri[at+1:], nil
}

// TerminalOrderState reports whether polling can stop for this Magma order_state.
func TerminalOrderState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "COMPLETED", "FAILED":
		return true
	default:
		return false
	}
}
