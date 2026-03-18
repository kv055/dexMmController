package hummingbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is a REST client for the Hummingbot Backend API (port 8000).
// Auth is HTTP Basic on every request — no token exchange required.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

// NewClient creates a new Hummingbot Backend API client.
// baseURL should be e.g. "http://localhost:8000".
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// get performs an authenticated GET and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	return c.do(req, path, out)
}

// post performs an authenticated POST with a JSON body and decodes the response into out.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, path, out)
}

func (c *Client) do(req *http.Request, path string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hummingbot: %s %s failed: %w", req.Method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hummingbot: %s %s returned HTTP %d", req.Method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Status returns the current running state of the Hummingbot API instance.
func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	var r StatusResponse
	if err := c.get(ctx, "/", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// PortfolioState returns the full portfolio state across all accounts and connectors.
func (c *Client) PortfolioState(ctx context.Context, req PortfolioStateRequest) (PortfolioState, error) {
	var state PortfolioState
	if err := c.post(ctx, "/portfolio/state", req, &state); err != nil {
		return nil, err
	}
	return state, nil
}

// BalanceForExchange returns the balance of a specific token on a specific connector.
// connector is the Hummingbot connector name (e.g. "kraken", "binance").
// token is the asset symbol (e.g. "USD", "BTC").
func (c *Client) BalanceForExchange(ctx context.Context, connector, token string) (*BalanceToken, error) {
	state, err := c.PortfolioState(ctx, PortfolioStateRequest{
		ConnectorNames: []string{connector},
		SkipGateway:    true,
		Refresh:        true,
	})
	if err != nil {
		return nil, err
	}
	for _, connectors := range state {
		for connName, tokens := range connectors {
			if connName != connector {
				continue
			}
			for _, t := range tokens {
				if t.Token == token {
					return &t, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("hummingbot: no balance found for %s on %s", token, connector)
}

// Trades returns a paginated list of trades, optionally filtered.
func (c *Client) Trades(ctx context.Context, req TradeFilterRequest) ([]TradeRecord, error) {
	var resp PaginatedResponse
	if err := c.post(ctx, "/trading/trades", req, &resp); err != nil {
		return nil, err
	}
	records := make([]TradeRecord, 0, len(resp.Data))
	for _, raw := range resp.Data {
		var r TradeRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("hummingbot: failed to decode trade record: %w", err)
		}
		r.Raw = raw
		records = append(records, r)
	}
	return records, nil
}

// TradesForExchange returns all trades for a specific connector/exchange.
// connector is the Hummingbot connector name (e.g. "kraken").
func (c *Client) TradesForExchange(ctx context.Context, connector string) ([]TradeRecord, error) {
	return c.Trades(ctx, TradeFilterRequest{
		ConnectorNames: []string{connector},
		Limit:          1000,
	})
}
