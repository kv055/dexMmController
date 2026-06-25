package coinbaseexchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.exchange.coinbase.com"

// Client talks to the Coinbase Exchange REST API using HMAC authentication.
type Client struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	passphrase string
	httpClient *http.Client
}

// NewClient creates a Coinbase Exchange API client.
func NewClient(baseURL, apiKey, apiSecret, passphrase string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		passphrase: passphrase,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Name returns the exchange or bridge name.
func (c *Client) Name() string {
	return "coinbase-exchange"
}

// AddressBookEntry is a saved (whitelisted) withdrawal address.
type AddressBookEntry struct {
	// ID identifies the address book entry.
	ID string `json:"id"`
	// Address is the wallet or peer address used by the address book entry.
	Address string `json:"address"`
	// Currency names the asset symbol for the address book entry (e.g. BTC).
	Currency string `json:"currency"`
	// Label is the user-defined label for the address book entry.
	Label string `json:"label"`
	// DestinationTag is the optional tag/memo for the address book entry.
	DestinationTag string `json:"destination_tag"`
	// Network names the blockchain network for the address book entry.
	Network string `json:"network"`
}

// AddressBook retrieves the saved (whitelisted) withdrawal addresses.
func (c *Client) AddressBook(ctx context.Context) ([]AddressBookEntry, error) {
	var resp []AddressBookEntry
	if err := c.getJSON(ctx, http.MethodGet, "/address-book", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, method, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature, err := sign(timestamp, method, path, c.apiSecret)
	if err != nil {
		return err
	}
	req.Header.Set("CB-ACCESS-KEY", c.apiKey)
	req.Header.Set("CB-ACCESS-SIGN", signature)
	req.Header.Set("CB-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("CB-ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("coinbase-exchange: %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("coinbase-exchange: reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coinbase-exchange: %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("coinbase-exchange: decode %s response: %w", path, err)
	}
	return nil
}

// sign builds the CB-ACCESS-SIGN header: base64(HMAC-SHA256(base64-decode(secret), timestamp+method+requestPath+body)).
func sign(timestamp, method, requestPath, secret string) (string, error) {
	secretBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(secret))
	if err != nil {
		return "", fmt.Errorf("coinbase-exchange: decode secret key: %w", err)
	}
	prehash := timestamp + strings.ToUpper(method) + requestPath
	mac := hmac.New(sha256.New, secretBytes)
	_, _ = mac.Write([]byte(prehash))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}
