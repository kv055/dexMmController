package robosats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 60 * time.Second

// DefaultCoordinatorURLs lists the known RoboSats mainnet coordinators. Robot
// tokens are coordinator-specific, so callers without a configured base URL
// can probe these in order. All coordinators are Tor onion services and
// require a SOCKS proxy (Config.ProxyURL).
var DefaultCoordinatorURLs = []string{
	"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
	"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
	"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
	"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
}

// Config represents a config.
type Config struct {
	// BaseURL is the base url value for the config.
	BaseURL string
	// AuthToken is the raw robot token. The client derives the
	// base91-encoded SHA256 hash RoboSats expects in the Authorization header.
	AuthToken string
	// ProxyURL is an optional SOCKS proxy (e.g. socks5h://127.0.0.1:9050),
	// required to reach the Tor onion coordinators.
	ProxyURL string
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
	proxyErr          error

	identityMu sync.Mutex
	identity   *Identity
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
	client := &Client{
		baseURL:           strings.TrimRight(cfg.BaseURL, "/"),
		authToken:         cfg.AuthToken,
		takeOfferPath:     defaultPath(cfg.TakeOfferPath, "/api/book/%s/take/"),
		openPositionsPath: defaultPath(cfg.OpenPositionsPath, "/api/orders/"),
		positionPath:      defaultPath(cfg.PositionPath, "/api/orders/%s/"),
		createOrderPath:   defaultPath(cfg.CreateOrderPath, "/api/orders/"),
		robotPath:         defaultPath(cfg.RobotPath, "/api/robot/"),
		http:              &http.Client{Timeout: defaultTimeout},
	}
	if proxy := strings.TrimSpace(cfg.ProxyURL); proxy != "" {
		// net/http understands the socks5 scheme and resolves hostnames at
		// the proxy, which onion addresses require; socks5h is an alias.
		proxyURL, err := url.Parse(strings.Replace(proxy, "socks5h://", "socks5://", 1))
		if err != nil {
			client.proxyErr = fmt.Errorf("robosats: invalid proxy URL: %w", err)
		} else {
			client.http.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		}
	}
	return client
}

// AuthHeaderToken returns the credential RoboSats expects in the
// Authorization header: the SHA256 hash of the raw robot token, base91-encoded.
func AuthHeaderToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base91Encode(digest[:])
}

// WithAuthToken configures the RoboSats authentication token.
func (c *Client) WithAuthToken(authToken string) *Client {
	return &Client{
		baseURL:           c.baseURL,
		authToken:         authToken,
		takeOfferPath:     c.takeOfferPath,
		openPositionsPath: c.openPositionsPath,
		positionPath:      c.positionPath,
		createOrderPath:   c.createOrderPath,
		robotPath:         c.robotPath,
		http:              c.http,
		proxyErr:          c.proxyErr,
	}
}

// robotIdentity lazily derives (and caches) the robot identity for the
// configured token. Coordinators require the full identity to create the
// robot on first contact.
func (c *Client) robotIdentity() (*Identity, error) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	if c.identity != nil {
		return c.identity, nil
	}
	identity, err := NewIdentity(c.authToken)
	if err != nil {
		return nil, err
	}
	c.identity = identity
	return identity, nil
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
	if c.proxyErr != nil {
		return c.proxyErr
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
		// Send the full identity, like the RoboSats web client: the
		// coordinator creates the robot on first contact and ignores the
		// keys once it exists.
		identity, err := c.robotIdentity()
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", identity.AuthHeader())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("robosats: %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if detail := readErrorDetail(resp.Body); detail != "" {
			return fmt.Errorf("robosats: %s %s returned HTTP %d (%s)", method, path, resp.StatusCode, detail)
		}
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

// readErrorDetail extracts the human-readable error message RoboSats returns
// in error bodies ({"bad_request": ...} or {"detail": ...}), or "" if absent.
func readErrorDetail(body io.Reader) string {
	var envelope struct {
		BadRequest string `json:"bad_request"`
		Detail     string `json:"detail"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 4096)).Decode(&envelope); err != nil {
		return ""
	}
	if envelope.BadRequest != "" {
		return envelope.BadRequest
	}
	return envelope.Detail
}

func defaultPath(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
