package coinbase

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.coinbase.com"
	requestHost    = "api.coinbase.com"
)

// Client represents a client.
type Client struct {
	baseURL    string
	keyName    string
	privateKey string
	httpClient *http.Client
}

// NewClient creates a client.
func NewClient(baseURL, keyName, privateKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		keyName:    keyName,
		privateKey: privateKey,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// AccountAmount is a currency amount from the Coinbase API.
type AccountAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// Account is a Coinbase Advanced Trade account row.
type Account struct {
	UUID             string        `json:"uuid"`
	Name             string        `json:"name"`
	Currency         string        `json:"currency"`
	AvailableBalance AccountAmount `json:"available_balance"`
	Hold             AccountAmount `json:"hold"`
	Type             string        `json:"type"`
	Active           bool          `json:"active"`
}

// DepositAddress is a Coinbase wallet deposit address.
type DepositAddress struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Network string `json:"network"`
}

// KeyPermissions reports Coinbase CDP API key capabilities.
type KeyPermissions struct {
	// CanView reports whether the key can view account data.
	CanView bool `json:"can_view"`
	// CanTrade reports whether the key can place trades.
	CanTrade bool `json:"can_trade"`
	// CanTransfer reports whether the key can transfer funds.
	CanTransfer bool `json:"can_transfer"`
}

// KeyPermissions retrieves Coinbase key permissions.
func (c *Client) KeyPermissions(ctx context.Context) (*KeyPermissions, error) {
	var permissions KeyPermissions
	if err := c.getJSON(ctx, http.MethodGet, "/api/v3/brokerage/key_permissions", nil, &permissions); err != nil {
		return nil, err
	}
	return &permissions, nil
}

// ListAccounts returns all Advanced Trade accounts for the API key.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	accounts := make([]Account, 0)
	cursor := ""
	for {
		path := "/api/v3/brokerage/accounts?limit=250"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Accounts []Account `json:"accounts"`
			HasNext  bool      `json:"has_next"`
			Cursor   string    `json:"cursor"`
		}
		if err := c.getJSON(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		accounts = append(accounts, page.Accounts...)
		if !page.HasNext || strings.TrimSpace(page.Cursor) == "" {
			break
		}
		cursor = page.Cursor
	}
	return accounts, nil
}

// BTCDepositAddresses returns on-chain BTC deposit addresses for the account.
func (c *Client) BTCDepositAddresses(ctx context.Context) ([]DepositAddress, error) {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	btcAccountID := findBTCAccountID(accounts)
	if btcAccountID == "" {
		btcAccountID, err = c.findV2BTCAccountID(ctx)
		if err != nil {
			return nil, err
		}
	}
	if btcAccountID == "" {
		return []DepositAddress{}, nil
	}

	path := "/v2/accounts/" + btcAccountID + "/addresses"
	var listed struct {
		Data []DepositAddress `json:"data"`
	}
	if err := c.getJSON(ctx, http.MethodGet, path, nil, &listed); err == nil {
		addresses := filterCoinbaseBTCAddresses(listed.Data)
		if len(addresses) > 0 {
			return addresses, nil
		}
	}

	var created struct {
		Data DepositAddress `json:"data"`
	}
	if err := c.getJSON(ctx, http.MethodPost, path, strings.NewReader("{}"), &created); err != nil {
		return nil, err
	}
	if strings.TrimSpace(created.Data.Address) == "" {
		return nil, fmt.Errorf("coinbase: deposit address response was empty")
	}
	return []DepositAddress{created.Data}, nil
}

func findBTCAccountID(accounts []Account) string {
	for _, account := range accounts {
		if !strings.EqualFold(strings.TrimSpace(account.Currency), "BTC") {
			continue
		}
		if id := strings.TrimSpace(account.UUID); id != "" {
			return id
		}
	}
	return ""
}

func (c *Client) findV2BTCAccountID(ctx context.Context) (string, error) {
	var listed struct {
		Data []struct {
			ID       string `json:"id"`
			Currency struct {
				Code string `json:"code"`
			} `json:"currency"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, http.MethodGet, "/v2/accounts", nil, &listed); err != nil {
		return "", err
	}
	for _, account := range listed.Data {
		if strings.EqualFold(strings.TrimSpace(account.Currency.Code), "BTC") {
			if id := strings.TrimSpace(account.ID); id != "" {
				return id, nil
			}
		}
	}
	return "", nil
}

func filterCoinbaseBTCAddresses(addresses []DepositAddress) []DepositAddress {
	out := make([]DepositAddress, 0, len(addresses))
	for _, row := range addresses {
		if strings.TrimSpace(row.Address) == "" {
			continue
		}
		out = append(out, row)
	}
	return out
}

func (c *Client) getJSON(ctx context.Context, method, path string, body io.Reader, dest any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	signPath := path
	if idx := strings.Index(path, "?"); idx >= 0 {
		signPath = path[:idx]
	}
	token, err := c.buildJWT(method, signPath)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("coinbase: %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("coinbase: reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coinbase: %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("coinbase: decode %s response: %w", path, err)
	}
	return nil
}

func (c *Client) buildJWT(method, path string) (string, error) {
	key, err := parseECPrivateKey(c.privateKey)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	uri := fmt.Sprintf("%s %s%s", method, requestHost, path)
	header := map[string]any{
		"alg":   "ES256",
		"typ":   "JWT",
		"kid":   c.keyName,
		"nonce": randomHex(16),
	}
	payload := map[string]any{
		"iss": "cdp",
		"sub": c.keyName,
		"nbf": now,
		"exp": now + 120,
		"uri": uri,
	}
	unsigned, err := encodeJWTParts(header, payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", fmt.Errorf("coinbase: sign jwt: %w", err)
	}
	signature := append(padBigInt(r, 32), padBigInt(s, 32)...)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseECPrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(normalized))
	if block == nil {
		return nil, fmt.Errorf("coinbase: decode EC private key PEM")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("coinbase: parse EC private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("coinbase: private key is not ECDSA")
	}
	return key, nil
}

func encodeJWTParts(header, payload any) (string, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON), nil
}

func padBigInt(v *big.Int, size int) []byte {
	out := make([]byte, size)
	raw := v.Bytes()
	copy(out[size-len(raw):], raw)
	return out
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
