package robosats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 20 * time.Second

type Config struct {
	BaseURL           string
	AuthToken         string
	TakeOfferPath     string
	OpenPositionsPath string
	PositionPath      string
	CreateOrderPath   string
}

type Client struct {
	baseURL           string
	authToken         string
	takeOfferPath     string
	openPositionsPath string
	positionPath      string
	createOrderPath   string
	http              *http.Client
}

type Position struct {
	ID            string          `json:"id"`
	OfferID       string          `json:"offer_id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Amount        json.Number     `json:"amount"`
	MinAmount     json.Number     `json:"min_amount"`
	MaxAmount     json.Number     `json:"max_amount"`
	Price         json.Number     `json:"price"`
	Premium       json.Number     `json:"premium"`
	PaymentMethod string          `json:"payment_method"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type CreatePositionParams struct {
	Type          int    `json:"type"`
	Currency      int    `json:"currency"`
	Amount        int64  `json:"amount,omitempty"`
	MinAmount     int64  `json:"min_amount,omitempty"`
	MaxAmount     int64  `json:"max_amount,omitempty"`
	Price         string `json:"price,omitempty"`
	Premium       string `json:"premium,omitempty"`
	PaymentMethod string `json:"payment_method,omitempty"`
}

func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:           strings.TrimRight(cfg.BaseURL, "/"),
		authToken:         cfg.AuthToken,
		takeOfferPath:     defaultPath(cfg.TakeOfferPath, "/api/book/%s/take/"),
		openPositionsPath: defaultPath(cfg.OpenPositionsPath, "/api/orders/"),
		positionPath:      defaultPath(cfg.PositionPath, "/api/orders/%s/"),
		createOrderPath:   defaultPath(cfg.CreateOrderPath, "/api/orders/"),
		http:              &http.Client{Timeout: defaultTimeout},
	}
}

func (c *Client) TakeOffer(ctx context.Context, offerID string) (*Position, error) {
	if offerID == "" {
		return nil, errors.New("robosats: offer ID is required")
	}

	var pos Position
	if err := c.request(ctx, http.MethodPost, fmt.Sprintf(c.takeOfferPath, offerID), nil, &pos); err != nil {
		return nil, err
	}
	return &pos, nil
}

func (c *Client) GetOpenPositions(ctx context.Context) ([]Position, error) {
	var positions []Position
	if err := c.request(ctx, http.MethodGet, c.openPositionsPath, nil, &positions); err != nil {
		return nil, err
	}
	return positions, nil
}

func (c *Client) GetPosition(ctx context.Context, positionID string) (*Position, error) {
	if positionID == "" {
		return nil, errors.New("robosats: position ID is required")
	}

	var pos Position
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf(c.positionPath, positionID), nil, &pos); err != nil {
		return nil, err
	}
	return &pos, nil
}

func (c *Client) CreatePosition(ctx context.Context, params CreatePositionParams) (*Position, error) {
	var pos Position
	if err := c.request(ctx, http.MethodPost, c.createOrderPath, params, &pos); err != nil {
		return nil, err
	}
	return &pos, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	if c.baseURL == "" {
		return errors.New("robosats: base URL is required")
	}

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return fmt.Errorf("robosats: encode request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, &payload)
	if err != nil {
		return fmt.Errorf("robosats: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Token "+c.authToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("robosats: %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("robosats: %s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("robosats: decode response: %w", err)
	}
	return nil
}

func defaultPath(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
