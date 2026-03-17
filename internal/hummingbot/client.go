package hummingbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Client is a REST client for the Hummingbot API.
// It handles token-based auth and re-authenticates automatically when the
// token expires.
type Client struct {
	baseURL  string
	username string
	password string

	mu       sync.Mutex
	token    string
	tokenExp time.Time

	http *http.Client
}

// NewClient creates a new Hummingbot REST client.
// baseURL should be e.g. "http://localhost:1985".
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// authenticate fetches a fresh token and caches it for 55 minutes
// (Hummingbot tokens are valid for 1 hour by default).
func (c *Client) authenticate(ctx context.Context) error {
	body, err := json.Marshal(AuthRequest{Username: c.username, Password: c.password})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hummingbot: auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hummingbot: auth returned HTTP %d", resp.StatusCode)
	}

	var ar AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("hummingbot: failed to decode auth response: %w", err)
	}
	if ar.Token == "" {
		return fmt.Errorf("hummingbot: auth returned empty token")
	}

	c.token = ar.Token
	c.tokenExp = time.Now().Add(55 * time.Minute)
	return nil
}

// token returns a valid bearer token, re-authenticating if necessary.
func (c *Client) bearerToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token == "" || time.Now().After(c.tokenExp) {
		if err := c.authenticate(ctx); err != nil {
			return "", err
		}
	}
	return c.token, nil
}

// get performs an authenticated GET request and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	token, err := c.bearerToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hummingbot: GET %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hummingbot: GET %s returned HTTP %d", path, resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// Status returns the current running state of the Hummingbot instance.
func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	var r StatusResponse
	if err := c.get(ctx, "/api/v1/status", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Balances returns all asset balances across all connected exchanges.
func (c *Client) Balances(ctx context.Context) ([]BalanceEntry, error) {
	var r BalanceResponse
	if err := c.get(ctx, "/api/v1/balance", &r); err != nil {
		return nil, err
	}
	return r.Balances, nil
}

// BalanceForExchange returns the balance of a specific asset on a specific exchange.
// Returns an error if the asset/exchange combination is not found.
func (c *Client) BalanceForExchange(ctx context.Context, exchange, asset string) (*BalanceEntry, error) {
	all, err := c.Balances(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range all {
		if b.Exchange == exchange && b.Asset == asset {
			return &b, nil
		}
	}
	return nil, fmt.Errorf("hummingbot: no balance found for %s on %s", asset, exchange)
}

// TradeHistory returns all trades recorded by Hummingbot.
func (c *Client) TradeHistory(ctx context.Context) ([]TradeRecord, error) {
	var r TradeHistoryResponse
	if err := c.get(ctx, "/api/v1/history", &r); err != nil {
		return nil, err
	}
	return r.Trades, nil
}

// TradesForExchange returns only the trades that belong to a given exchange.
func (c *Client) TradesForExchange(ctx context.Context, exchange string) ([]TradeRecord, error) {
	all, err := c.TradeHistory(ctx)
	if err != nil {
		return nil, err
	}
	var filtered []TradeRecord
	for _, t := range all {
		if t.Exchange == exchange {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// WithdrawalHistory returns all withdrawals recorded by Hummingbot.
func (c *Client) WithdrawalHistory(ctx context.Context) ([]WithdrawalRecord, error) {
	var r WithdrawalHistoryResponse
	if err := c.get(ctx, "/api/v1/withdrawals", &r); err != nil {
		return nil, err
	}
	return r.Withdrawals, nil
}

// WithdrawalByTxHash looks up a withdrawal by its on-chain transaction hash.
func (c *Client) WithdrawalByTxHash(ctx context.Context, txHash string) (*WithdrawalRecord, error) {
	all, err := c.WithdrawalHistory(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range all {
		if w.TxHash == txHash {
			return &w, nil
		}
	}
	return nil, fmt.Errorf("hummingbot: no withdrawal found with tx hash %q", txHash)
}
