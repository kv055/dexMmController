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

// Config represents a config.
type Config struct {
	// BaseURL is the base url value for the config.
	BaseURL string
	// AuthToken is the auth token value for the config.
	AuthToken string
	// TakeOfferPath is the take offer path value for the config.
	TakeOfferPath string
	// OpenPositionsPath is the open positions path value for the config.
	OpenPositionsPath string
	// PositionPath is the position path value for the config.
	PositionPath string
	// CreateOrderPath is the create order path value for the config.
	CreateOrderPath string
	// RobotPath is the robot path value for the config.
	RobotPath string
}

// Client represents a client.
type Client struct {
	baseURL           string
	authToken         string
	takeOfferPath     string
	openPositionsPath string
	positionPath      string
	createOrderPath   string
	robotPath         string
	http              *http.Client
}

// Robot identifies a robot.
type Robot map[string]any

// Position represents a position.
type Position struct {
	// ID identifies the position.
	ID string `json:"id"`
	// OfferID is the offer id value for the position.
	OfferID string `json:"offer_id"`
	// Type is the type value for the position.
	Type string `json:"type"`
	// Status is the current status of the position.
	Status string `json:"status"`
	// Amount is the amount value for the position.
	Amount json.Number `json:"amount"`
	// MinAmount is the min amount value for the position.
	MinAmount json.Number `json:"min_amount"`
	// MaxAmount is the max amount value for the position.
	MaxAmount json.Number `json:"max_amount"`
	// Price is the price value used by the position.
	Price json.Number `json:"price"`
	// Premium is the premium value for the position.
	Premium json.Number `json:"premium"`
	// PaymentMethod is the payment method value for the position.
	PaymentMethod string `json:"payment_method"`
	// Raw preserves additional source data for the position.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// CreatePositionParams represents a create position params.
type CreatePositionParams struct {
	// Type is the type value for the create position params.
	Type int `json:"type"`
	// Currency names the asset or network for the create position params.
	Currency int `json:"currency"`
	// Amount is the amount value for the create position params.
	Amount int64 `json:"amount,omitempty"`
	// MinAmount is the min amount value for the create position params.
	MinAmount int64 `json:"min_amount,omitempty"`
	// MaxAmount is the max amount value for the create position params.
	MaxAmount int64 `json:"max_amount,omitempty"`
	// Price is the price value used by the create position params.
	Price string `json:"price,omitempty"`
	// Premium is the premium value for the create position params.
	Premium string `json:"premium,omitempty"`
	// PaymentMethod is the payment method value for the create position params.
	PaymentMethod string `json:"payment_method,omitempty"`
}

// NewClient creates a client.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:           strings.TrimRight(cfg.BaseURL, "/"),
		authToken:         cfg.AuthToken,
		takeOfferPath:     defaultPath(cfg.TakeOfferPath, "/api/book/%s/take/"),
		openPositionsPath: defaultPath(cfg.OpenPositionsPath, "/api/orders/"),
		positionPath:      defaultPath(cfg.PositionPath, "/api/orders/%s/"),
		createOrderPath:   defaultPath(cfg.CreateOrderPath, "/api/orders/"),
		robotPath:         defaultPath(cfg.RobotPath, "/api/robot/"),
		http:              &http.Client{Timeout: defaultTimeout},
	}
}

// WithAuthToken configures the RoboSats authentication token.
func (c *Client) WithAuthToken(authToken string) *Client {
	clone := *c
	clone.authToken = authToken
	return &clone
}

// GetRobot retrieves a robot.
func (c *Client) GetRobot(ctx context.Context) (Robot, error) {
	var robot Robot
	if err := c.request(ctx, http.MethodGet, c.robotPath, nil, &robot); err != nil {
		return nil, err
	}
	return robot, nil
}

// TakeOffer accepts an existing DEX offer.
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

// GetOpenPositions retrieves an open positions.
func (c *Client) GetOpenPositions(ctx context.Context) ([]Position, error) {
	var positions []Position
	if err := c.request(ctx, http.MethodGet, c.openPositionsPath, nil, &positions); err != nil {
		return nil, err
	}
	return positions, nil
}

// GetPosition retrieves a position.
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

// CreatePosition creates a position.
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
