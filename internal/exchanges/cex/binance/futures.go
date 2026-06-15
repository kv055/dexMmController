package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/exchanges/cex"
)

const defaultFuturesBaseURL = "https://fapi.binance.com"

// FuturesClient places orders on Binance USDⓈ-M Futures (fapi.binance.com).
// It reuses the same API key as the spot client when BINANCE_FUTURES_* vars are absent,
// provided that key has futures trading enabled (enableFutures=true).
type FuturesClient struct {
	baseURL   string
	apiKey    string
	secretKey string
	http      *http.Client
}

// NewFuturesClient creates a FuturesClient from environment variables.
func NewFuturesClient() *FuturesClient {
	return &FuturesClient{
		baseURL:   defaultFuturesBaseURL,
		apiKey:    firstBNBEnv("BINANCE_FUTURES_API_KEY", "BINANCE_API_KEY"),
		secretKey: firstBNBEnv("BINANCE_FUTURES_API_SECRET", "BINANCE_API_SECRET"),
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func firstBNBEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// PlaceOrder places a market order on Binance USDⓈ-M Futures.
// symbol is e.g. "BTCUSDT". side must be "short" or "long".
// leverage is applied best-effort (logged on failure, order not aborted).
func (c *FuturesClient) PlaceOrder(ctx context.Context, symbol, side string, btcQty float64, leverage int) (*cex.FuturesOrderResult, error) {
	if leverage > 0 {
		if err := c.setLeverage(ctx, symbol, leverage); err != nil {
			// Non-fatal: leverage may already be set, or account may have restrictions.
			// Log but continue so the order is still placed.
			fmt.Printf("binance futures: set leverage warning: %v\n", err)
		}
	}

	bnbSide := "SELL"
	if side == "long" {
		bnbSide = "BUY"
	}

	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", bnbSide)
	params.Set("type", "MARKET")
	params.Set("quantity", strconv.FormatFloat(btcQty, 'f', 3, 64))

	rawResp, err := c.signedPost(ctx, "/fapi/v1/order", params)
	if err != nil {
		return nil, fmt.Errorf("binance futures order: %w", err)
	}

	var resp binanceFuturesOrderResponse
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		return nil, fmt.Errorf("binance futures order decode: %w", err)
	}

	return &cex.FuturesOrderResult{
		Venue:      "binance_futures",
		Instrument: symbol,
		Side:       side,
		OrderID:    strconv.FormatInt(resp.OrderID, 10),
		Status:     strings.ToLower(resp.Status),
		Raw:        rawResp,
	}, nil
}

type binanceFuturesOrderResponse struct {
	OrderID int64  `json:"orderId"`
	Status  string `json:"status"`
}

type binanceFuturesLeverageResponse struct {
	Leverage  int    `json:"leverage"`
	Symbol    string `json:"symbol"`
	MaxNotVal string `json:"maxNotionalValue"`
}

func (c *FuturesClient) setLeverage(ctx context.Context, symbol string, leverage int) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("leverage", strconv.Itoa(leverage))

	raw, err := c.signedPost(ctx, "/fapi/v1/leverage", params)
	if err != nil {
		return err
	}
	var resp binanceFuturesLeverageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Symbol == "" {
		return fmt.Errorf("unexpected response: %s", string(raw))
	}
	return nil
}

func (c *FuturesClient) signedPost(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	queryString := params.Encode()
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(queryString))
	params.Set("signature", hex.EncodeToString(mac.Sum(nil)))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
