package kraken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/exchangeconnectors/cex"
)

const defaultFuturesBaseURL = "https://futures.kraken.com"

// FuturesClient places orders on Kraken Futures (futures.kraken.com).
type FuturesClient struct {
	baseURL   string
	apiKey    string
	apiSecret string
	http      *http.Client
}

// NewFuturesClient creates a FuturesClient from environment variables.
// KRAKEN_FUTURES_API_BASE_URL overrides the production endpoint (useful for testing).
// Falls back to KRAKEN_API_KEY / KRAKEN_API_SECRET when the FUTURES-specific vars are absent.
func NewFuturesClient() *FuturesClient {
	baseURL := os.Getenv("KRAKEN_FUTURES_API_BASE_URL")
	if baseURL == "" {
		baseURL = defaultFuturesBaseURL
	}
	apiKey := firstFuturesEnv("KRAKEN_FUTURES_API_KEY", "KRAKEN_API_KEY")
	apiSecret := firstFuturesEnv("KRAKEN_FUTURES_API_SECRET", "KRAKEN_API_SECRET")
	return &FuturesClient{
		baseURL:   baseURL,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func firstFuturesEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// PlaceOrder places a market order on Kraken Futures for the given symbol.
// symbol is e.g. "PI_XBTUSD". side must be "short" or "long".
// btcNotional is the BTC amount to hedge; contracts are sized via the live mark price.
func (c *FuturesClient) PlaceOrder(ctx context.Context, symbol, side string, btcNotional float64) (*cex.FuturesOrderResult, error) {
	markPrice, err := c.fetchMarkPrice(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("kraken futures mark price: %w", err)
	}
	contracts := int64(math.Round(btcNotional * markPrice))
	if contracts <= 0 {
		return nil, fmt.Errorf("kraken futures: computed contract size is zero (btcNotional=%.8f markPrice=%.2f)", btcNotional, markPrice)
	}

	krakenSide := "sell"
	if side == "long" {
		krakenSide = "buy"
	}

	body := url.Values{}
	body.Set("orderType", "mkt")
	body.Set("symbol", symbol)
	body.Set("side", krakenSide)
	body.Set("size", strconv.FormatInt(contracts, 10))
	postData := body.Encode()

	path := "/derivatives/api/v3/sendorder"
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	rawBody, err := c.signedPost(ctx, path, postData, nonce)
	if err != nil {
		return nil, fmt.Errorf("kraken futures sendorder: %w", err)
	}

	var resp krakenSendOrderResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return nil, fmt.Errorf("kraken futures sendorder decode: %w", err)
	}
	if resp.Result != "success" {
		return nil, fmt.Errorf("kraken futures sendorder: result=%q error=%q", resp.Result, resp.Error)
	}

	return &cex.FuturesOrderResult{
		Venue:      "kraken_futures",
		Instrument: symbol,
		Side:       side,
		OrderID:    resp.SendStatus.OrderID,
		Status:     resp.SendStatus.Status,
		Raw:        rawBody,
	}, nil
}

// krakenTickerResponse holds the relevant part of the /tickers/<symbol> response.
type krakenTickerResponse struct {
	Tickers []struct {
		Tag       string  `json:"tag"`
		MarkPrice float64 `json:"markPrice"`
	} `json:"tickers"`
}

func (c *FuturesClient) fetchMarkPrice(ctx context.Context, symbol string) (float64, error) {
	u := c.baseURL + "/derivatives/api/v3/tickers/" + url.PathEscape(symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out krakenTickerResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	if len(out.Tickers) == 0 {
		return 0, fmt.Errorf("no tickers in response")
	}
	if out.Tickers[0].MarkPrice <= 0 {
		return 0, fmt.Errorf("mark price is zero or negative")
	}
	return out.Tickers[0].MarkPrice, nil
}

// krakenOpenPositionsResponse is the relevant subset of /openpositions.
type krakenOpenPositionsResponse struct {
	Result        string `json:"result"`
	Error         string `json:"error"`
	OpenPositions []struct {
		Side     string  `json:"side"`
		Symbol   string  `json:"symbol"`
		Price    float64 `json:"price"`
		Size     float64 `json:"size"`
		FillTime string  `json:"fillTime"`
	} `json:"openPositions"`
}

// CurrentPosition fetches the live PI_XBTUSD position synchronously via the signed
// /openpositions REST endpoint. Used as an idempotency guard after a hedge attempt
// errors: it answers "did my order actually open?" The Kraken Futures REST reports
// position size in USD contracts, so BTC size is derived as size/price. A
// flat/absent position yields Side:"flat".
func (c *FuturesClient) CurrentPosition(ctx context.Context) (cex.HedgePositionUpdate, error) {
	path := "/derivatives/api/v3/openpositions"
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	raw, err := c.signedGet(ctx, path, nonce)
	if err != nil {
		return cex.HedgePositionUpdate{}, fmt.Errorf("kraken futures openpositions: %w", err)
	}

	var resp krakenOpenPositionsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return cex.HedgePositionUpdate{}, fmt.Errorf("kraken futures openpositions decode: %w", err)
	}
	if resp.Result != "success" {
		return cex.HedgePositionUpdate{}, fmt.Errorf("kraken futures openpositions: result=%q error=%q", resp.Result, resp.Error)
	}

	update := cex.HedgePositionUpdate{
		Venue:      "kraken_futures",
		Instrument: hedgeInstrument,
		Side:       "flat",
		Currency:   "USD",
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Connected:  true,
	}
	for _, pos := range resp.OpenPositions {
		if !strings.EqualFold(pos.Symbol, hedgeInstrument) {
			continue
		}
		update.Side = strings.ToLower(strings.TrimSpace(pos.Side))
		update.EntryPrice = pos.Price
		if pos.Price > 0 {
			update.SizeBTC = absFloat(pos.Size / pos.Price)
		}
		return update, nil
	}
	return update, nil
}

func (c *FuturesClient) signedGet(ctx context.Context, path, nonce string) ([]byte, error) {
	// Kraken Futures signs sha256(postData + nonce + path); a GET has empty postData.
	hash := sha256.Sum256([]byte("" + nonce + path))
	secretBytes, err := base64.StdEncoding.DecodeString(c.apiSecret)
	if err != nil {
		return nil, fmt.Errorf("decode api secret: %w", err)
	}
	mac := hmac.New(sha512.New, secretBytes)
	mac.Write(hash[:])
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APIKey", c.apiKey)
	req.Header.Set("Nonce", nonce)
	req.Header.Set("Authent", sig)

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

type krakenSendOrderResponse struct {
	Result     string `json:"result"`
	Error      string `json:"error"`
	SendStatus struct {
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
	} `json:"sendStatus"`
}

func (c *FuturesClient) signedPost(ctx context.Context, path, postData, nonce string) ([]byte, error) {
	hash := sha256.Sum256([]byte(postData + nonce + path))
	secretBytes, err := base64.StdEncoding.DecodeString(c.apiSecret)
	if err != nil {
		return nil, fmt.Errorf("decode api secret: %w", err)
	}
	mac := hmac.New(sha512.New, secretBytes)
	mac.Write(hash[:])
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, strings.NewReader(postData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("APIKey", c.apiKey)
	req.Header.Set("Nonce", nonce)
	req.Header.Set("Authent", sig)

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
