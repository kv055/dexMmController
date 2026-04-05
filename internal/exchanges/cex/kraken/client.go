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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.kraken.com"

type Client struct {
	baseURL    string
	apiKey     string
	secretKey  string
	httpClient *http.Client
}

type DepositMethod struct {
	Method     string `json:"method"`
	Limit      bool   `json:"limit"`
	GenAddress bool   `json:"gen-address"`
}

type DepositAddress struct {
	Address  string `json:"address"`
	ExpireTM string `json:"expiretm"`
	New      bool   `json:"new,omitempty"`
}

type DepositAddressRequest struct {
	Asset     string
	Method    string
	CreateNew bool
	Amount    string
}

type WithdrawRequest struct {
	Asset   string
	Key     string
	Address string
	Method  string
	Amount  string
}

type WithdrawMethod struct {
	Method string `json:"method"`
	Limit  bool   `json:"limit"`
	Fee    string `json:"fee"`
}

type WithdrawResponse struct {
	RefID string `json:"refid"`
}

type DepositStatusRequest struct {
	Asset     string
	Method    string
	StartTime *time.Time
	EndTime   *time.Time
}

type DepositStatusRecord struct {
	Method string `json:"method"`
	Asset  string `json:"asset"`
	RefID  string `json:"refid"`
	TxID   string `json:"txid"`
	Info   string `json:"info"`
	Amount string `json:"amount"`
	Fee    string `json:"fee"`
	Time   int64  `json:"time"`
	Status string `json:"status"`
}

type WithdrawStatusRequest struct {
	Asset     string
	Method    string
	StartTime *time.Time
	EndTime   *time.Time
}

type WithdrawStatusRecord struct {
	Method string `json:"method"`
	Asset  string `json:"asset"`
	RefID  string `json:"refid"`
	TxID   string `json:"txid"`
	Info   string `json:"info"`
	Amount string `json:"amount"`
	Fee    string `json:"fee"`
	Time   int64  `json:"time"`
	Status string `json:"status"`
}

type apiEnvelope[T any] struct {
	Error  []string `json:"error"`
	Result T        `json:"result"`
}

func NewClient(baseURL, apiKey, secretKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) Name() string {
	return "kraken"
}

func (c *Client) GetDepositMethods(ctx context.Context, asset string) ([]DepositMethod, error) {
	form := url.Values{}
	form.Set("asset", asset)

	var resp apiEnvelope[[]DepositMethod]
	if err := c.privateRequest(ctx, "/0/private/DepositMethods", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) GetDepositAddresses(ctx context.Context, asset, method string, createNew bool) ([]DepositAddress, error) {
	return c.GetDepositAddressesWithOptions(ctx, DepositAddressRequest{
		Asset:     asset,
		Method:    method,
		CreateNew: createNew,
	})
}

func (c *Client) GetDepositAddressesWithOptions(ctx context.Context, req DepositAddressRequest) ([]DepositAddress, error) {
	form := url.Values{}
	form.Set("asset", req.Asset)
	form.Set("method", req.Method)
	if req.CreateNew {
		form.Set("new", "true")
	}
	if req.Amount != "" {
		form.Set("amount", req.Amount)
	}

	var resp apiEnvelope[[]DepositAddress]
	if err := c.privateRequest(ctx, "/0/private/DepositAddresses", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) GetWithdrawMethods(ctx context.Context, asset string) ([]WithdrawMethod, error) {
	form := url.Values{}
	form.Set("asset", asset)

	var resp apiEnvelope[[]WithdrawMethod]
	if err := c.privateRequest(ctx, "/0/private/WithdrawMethods", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	form := url.Values{}
	form.Set("asset", req.Asset)
	form.Set("amount", req.Amount)
	if req.Key != "" {
		form.Set("key", req.Key)
	}
	if req.Address != "" {
		form.Set("address", req.Address)
	}
	if req.Method != "" {
		form.Set("method", req.Method)
	}

	var resp apiEnvelope[WithdrawResponse]
	if err := c.privateRequest(ctx, "/0/private/Withdraw", form, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

func (c *Client) GetDepositStatus(ctx context.Context, req DepositStatusRequest) ([]DepositStatusRecord, error) {
	form := url.Values{}
	form.Set("asset", req.Asset)
	if req.Method != "" {
		form.Set("method", req.Method)
	}
	if req.StartTime != nil {
		form.Set("start", strconv.FormatInt(req.StartTime.Unix(), 10))
	}
	if req.EndTime != nil {
		form.Set("end", strconv.FormatInt(req.EndTime.Unix(), 10))
	}

	var resp apiEnvelope[[]DepositStatusRecord]
	if err := c.privateRequest(ctx, "/0/private/DepositStatus", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) GetWithdrawStatus(ctx context.Context, req WithdrawStatusRequest) ([]WithdrawStatusRecord, error) {
	form := url.Values{}
	form.Set("asset", req.Asset)
	if req.Method != "" {
		form.Set("method", req.Method)
	}
	if req.StartTime != nil {
		form.Set("start", strconv.FormatInt(req.StartTime.Unix(), 10))
	}
	if req.EndTime != nil {
		form.Set("end", strconv.FormatInt(req.EndTime.Unix(), 10))
	}

	var resp apiEnvelope[[]WithdrawStatusRecord]
	if err := c.privateRequest(ctx, "/0/private/WithdrawStatus", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) privateRequest(ctx context.Context, path string, form url.Values, out any) error {
	if form == nil {
		form = url.Values{}
	}
	nonce := strconv.FormatInt(time.Now().UnixMilli(), 10)
	form.Set("nonce", nonce)
	encodedForm := form.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(encodedForm))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("API-Key", c.apiKey)

	signature, err := sign(path, nonce, encodedForm, c.secretKey)
	if err != nil {
		return err
	}
	req.Header.Set("API-Sign", signature)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kraken: POST %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("kraken: reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kraken: POST %s returned HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("kraken: decode response: %w", err)
	}

	switch envelope := out.(type) {
	case *apiEnvelope[[]DepositMethod]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[[]DepositAddress]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[[]WithdrawMethod]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[WithdrawResponse]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[[]DepositStatusRecord]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[[]WithdrawStatusRecord]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	}
	return nil
}

func sign(path, nonce, encodedForm, secret string) (string, error) {
	secretBytes, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("kraken: decode secret key: %w", err)
	}

	sha := sha256.Sum256([]byte(nonce + encodedForm))
	mac := hmac.New(sha512.New, secretBytes)
	_, _ = mac.Write(append([]byte(path), sha[:]...))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}
