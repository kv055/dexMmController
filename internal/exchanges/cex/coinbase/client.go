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
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.coinbase.com"
	requestHost    = "api.coinbase.com"
)

type Client struct {
	baseURL    string
	keyName    string
	privateKey string
	httpClient *http.Client
}

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

func (c *Client) KeyPermissions(ctx context.Context) error {
	const path = "/api/v3/brokerage/key_permissions"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	token, err := c.buildJWT(http.MethodGet, path)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("coinbase: GET %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("coinbase: GET %s returned HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
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
