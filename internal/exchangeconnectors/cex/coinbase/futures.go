package coinbase

import (
	"bytes"
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

	"dexMmController/internal/exchangeconnectors/cex"
)

const defaultINTXBaseURL = "https://api.international.coinbase.com"

// FuturesClient places orders on Coinbase International (INTX) perpetual futures.
type FuturesClient struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	passphrase string
	http       *http.Client
}

// NewFuturesClient creates a FuturesClient from database-loaded credentials.
func NewFuturesClient(apiKey, apiSecret, passphrase string) *FuturesClient {
	return NewFuturesClientWithBaseURL(defaultINTXBaseURL, apiKey, apiSecret, passphrase)
}

// NewFuturesClientWithBaseURL creates a FuturesClient pointed at baseURL (tests / overrides).
func NewFuturesClientWithBaseURL(baseURL, apiKey, apiSecret, passphrase string) *FuturesClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultINTXBaseURL
	}
	return &FuturesClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		apiSecret:  strings.TrimSpace(apiSecret),
		passphrase: strings.TrimSpace(passphrase),
		http:       &http.Client{Timeout: 15 * time.Second},
	}
}

// CheckAccess verifies the INTX API key can list portfolios (hedge permission probe).
func (c *FuturesClient) CheckAccess(ctx context.Context) error {
	_, err := c.ListPortfolios(ctx)
	return err
}

// ListPortfolios returns INTX portfolio UUIDs for the authenticated key.
func (c *FuturesClient) ListPortfolios(ctx context.Context) ([]string, error) {
	raw, err := c.signedGet(ctx, "/api/v1/portfolios")
	if err != nil {
		return nil, err
	}
	ids, err := decodeCoinbasePortfolios(raw)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no portfolios found")
	}
	return ids, nil
}

// PlaceOrder places a market IOC order on Coinbase INTX for the given instrument.
// instrument is e.g. "BTC-PERP". side must be "short" or "long".
func (c *FuturesClient) PlaceOrder(ctx context.Context, instrument, side string, btcSize float64) (*cex.FuturesOrderResult, error) {
	portfolioID, err := c.fetchPortfolioID(ctx)
	if err != nil {
		return nil, fmt.Errorf("coinbase intx portfolio: %w", err)
	}

	intxSide := "SELL"
	if side == "long" {
		intxSide = "BUY"
	}

	orderBody := map[string]any{
		"portfolio":     portfolioID,
		"instrument_id": instrument,
		"side":          intxSide,
		"type":          "MARKET",
		"size":          strconv.FormatFloat(btcSize, 'f', 8, 64),
		"tif":           "IOC",
	}
	bodyBytes, err := json.Marshal(orderBody)
	if err != nil {
		return nil, err
	}

	path := "/api/v1/orders"
	rawResp, err := c.signedPost(ctx, path, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("coinbase intx orders: %w", err)
	}

	var resp coinbaseINTXOrderResponse
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		return nil, fmt.Errorf("coinbase intx orders decode: %w", err)
	}
	if resp.Order.OrderID == "" {
		return nil, fmt.Errorf("coinbase intx orders: empty order_id in response")
	}

	return &cex.FuturesOrderResult{
		Venue:      "coinbase_futures",
		Instrument: instrument,
		Side:       side,
		OrderID:    resp.Order.OrderID,
		Status:     strings.ToLower(resp.Order.Status),
		Raw:        rawResp,
	}, nil
}

// CurrentPosition fetches the live BTC-PERP position synchronously (a one-shot of
// the same signed REST call the position stream polls). Used as an idempotency
// guard after a hedge attempt errors: it answers "did my order actually open?"
// A flat/absent position yields Side:"flat".
func (c *FuturesClient) CurrentPosition(ctx context.Context) (cex.HedgePositionUpdate, error) {
	return c.fetchPosition(ctx)
}

type coinbaseINTXPortfolio struct {
	PortfolioUUID string `json:"portfolio_uuid"`
	PortfolioID   string `json:"portfolio_id"`
	ID            string `json:"id"`
}

func (p coinbaseINTXPortfolio) id() string {
	for _, candidate := range []string{p.PortfolioUUID, p.PortfolioID, p.ID} {
		if id := strings.TrimSpace(candidate); id != "" {
			return id
		}
	}
	return ""
}

func decodeCoinbasePortfolios(raw []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("decode portfolios: empty response")
	}

	var rows []coinbaseINTXPortfolio
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("decode portfolios: %w", err)
		}
	case '{':
		var wrapped struct {
			Portfolios []coinbaseINTXPortfolio `json:"portfolios"`
		}
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil, fmt.Errorf("decode portfolios: %w", err)
		}
		rows = wrapped.Portfolios
	default:
		return nil, fmt.Errorf("decode portfolios: unexpected JSON")
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := row.id(); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (c *FuturesClient) fetchPortfolioID(ctx context.Context) (string, error) {
	ids, err := c.ListPortfolios(ctx)
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

type coinbaseINTXOrderResponse struct {
	Order struct {
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
	} `json:"order"`
}

func (c *FuturesClient) signedGet(ctx context.Context, path string) ([]byte, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil)
}

func (c *FuturesClient) signedPost(ctx context.Context, path string, body []byte) ([]byte, error) {
	return c.doRequest(ctx, http.MethodPost, path, body)
}

func (c *FuturesClient) doRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	bodyStr := ""
	if len(body) > 0 {
		bodyStr = string(body)
	}

	secretBytes, err := base64.StdEncoding.DecodeString(c.apiSecret)
	if err != nil {
		return nil, fmt.Errorf("decode api secret: %w", err)
	}
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(timestamp + method + path + bodyStr))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("CB-ACCESS-KEY", c.apiKey)
	req.Header.Set("CB-ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("CB-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("CB-ACCESS-SIGN", sig)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
