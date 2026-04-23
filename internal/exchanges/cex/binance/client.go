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
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.binance.com"

type Client struct {
	baseURL    string
	apiKey     string
	secretKey  string
	httpClient *http.Client
}

type DepositAddressResponse struct {
	Address   string `json:"address"`
	Coin      string `json:"coin"`
	Tag       string `json:"tag"`
	URL       string `json:"url"`
	IsDefault int    `json:"isDefault"`
}

type WithdrawRequest struct {
	Coin               string
	Address            string
	Amount             string
	Network            string
	AddressTag         string
	WithdrawOrderID    string
	TransactionFeeFlag *bool
	Name               string
	WalletType         *int
	RecvWindow         int64
}

type WithdrawResponse struct {
	ID string `json:"id"`
}

type DepositHistoryParams struct {
	Coin       string
	Status     *int
	StartTime  *time.Time
	EndTime    *time.Time
	Offset     *int
	Limit      *int
	RecvWindow int64
}

type DepositRecord struct {
	ID            string `json:"id"`
	Amount        string `json:"amount"`
	Coin          string `json:"coin"`
	Network       string `json:"network"`
	Status        int    `json:"status"`
	Address       string `json:"address"`
	AddressTag    string `json:"addressTag"`
	TxID          string `json:"txId"`
	InsertTime    int64  `json:"insertTime"`
	TransferType  int    `json:"transferType"`
	ConfirmTimes  string `json:"confirmTimes"`
	UnlockConfirm string `json:"unlockConfirm"`
}

type WithdrawHistoryParams struct {
	Coin            string
	WithdrawOrderID string
	Status          *int
	StartTime       *time.Time
	EndTime         *time.Time
	Offset          *int
	Limit           *int
	RecvWindow      int64
}

type WithdrawRecord struct {
	ID              string `json:"id"`
	Amount          string `json:"amount"`
	TransactionFee  string `json:"transactionFee"`
	Coin            string `json:"coin"`
	Status          int    `json:"status"`
	Address         string `json:"address"`
	ApplyTime       string `json:"applyTime"`
	Network         string `json:"network"`
	TransferType    int    `json:"transferType"`
	WithdrawOrderID string `json:"withdrawOrderId"`
	TxID            string `json:"txId"`
}

type AccountInfo struct {
	CanTrade    bool `json:"canTrade"`
	CanWithdraw bool `json:"canWithdraw"`
	CanDeposit  bool `json:"canDeposit"`
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
	return "binance"
}

func (c *Client) AccountInfo(ctx context.Context) (*AccountInfo, error) {
	var resp AccountInfo
	if err := c.signedRequest(ctx, http.MethodGet, "/api/v3/account", nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetDepositAddress(ctx context.Context, coin, network string) (*DepositAddressResponse, error) {
	query := url.Values{}
	query.Set("coin", coin)
	if network != "" {
		query.Set("network", network)
	}

	var resp DepositAddressResponse
	if err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/capital/deposit/address", query, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ApplyWithdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	body := url.Values{}
	body.Set("coin", req.Coin)
	body.Set("address", req.Address)
	body.Set("amount", req.Amount)
	if req.Network != "" {
		body.Set("network", req.Network)
	}
	if req.AddressTag != "" {
		body.Set("addressTag", req.AddressTag)
	}
	if req.WithdrawOrderID != "" {
		body.Set("withdrawOrderId", req.WithdrawOrderID)
	}
	if req.TransactionFeeFlag != nil {
		body.Set("transactionFeeFlag", strconv.FormatBool(*req.TransactionFeeFlag))
	}
	if req.Name != "" {
		body.Set("name", req.Name)
	}
	if req.WalletType != nil {
		body.Set("walletType", strconv.Itoa(*req.WalletType))
	}
	if req.RecvWindow > 0 {
		body.Set("recvWindow", strconv.FormatInt(req.RecvWindow, 10))
	}

	var resp WithdrawResponse
	if err := c.signedRequest(ctx, http.MethodPost, "/sapi/v1/capital/withdraw/apply", body, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetDepositHistory(ctx context.Context, params DepositHistoryParams) ([]DepositRecord, error) {
	query := url.Values{}
	if params.Coin != "" {
		query.Set("coin", params.Coin)
	}
	if params.Status != nil {
		query.Set("status", strconv.Itoa(*params.Status))
	}
	if params.StartTime != nil {
		query.Set("startTime", strconv.FormatInt(params.StartTime.UnixMilli(), 10))
	}
	if params.EndTime != nil {
		query.Set("endTime", strconv.FormatInt(params.EndTime.UnixMilli(), 10))
	}
	if params.Offset != nil {
		query.Set("offset", strconv.Itoa(*params.Offset))
	}
	if params.Limit != nil {
		query.Set("limit", strconv.Itoa(*params.Limit))
	}
	if params.RecvWindow > 0 {
		query.Set("recvWindow", strconv.FormatInt(params.RecvWindow, 10))
	}

	var resp []DepositRecord
	if err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/capital/deposit/hisrec", query, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) GetWithdrawHistory(ctx context.Context, params WithdrawHistoryParams) ([]WithdrawRecord, error) {
	query := url.Values{}
	if params.Coin != "" {
		query.Set("coin", params.Coin)
	}
	if params.WithdrawOrderID != "" {
		query.Set("withdrawOrderId", params.WithdrawOrderID)
	}
	if params.Status != nil {
		query.Set("status", strconv.Itoa(*params.Status))
	}
	if params.StartTime != nil {
		query.Set("startTime", strconv.FormatInt(params.StartTime.UnixMilli(), 10))
	}
	if params.EndTime != nil {
		query.Set("endTime", strconv.FormatInt(params.EndTime.UnixMilli(), 10))
	}
	if params.Offset != nil {
		query.Set("offset", strconv.Itoa(*params.Offset))
	}
	if params.Limit != nil {
		query.Set("limit", strconv.Itoa(*params.Limit))
	}
	if params.RecvWindow > 0 {
		query.Set("recvWindow", strconv.FormatInt(params.RecvWindow, 10))
	}

	var resp []WithdrawRecord
	if err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/capital/withdraw/history", query, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) signedRequest(ctx context.Context, method, path string, params url.Values, headers http.Header, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	queryString := params.Encode()
	signature := sign(queryString, c.secretKey)
	params.Set("signature", signature)

	var body io.Reader
	fullURL := c.baseURL + path
	switch method {
	case http.MethodGet:
		fullURL += "?" + params.Encode()
	case http.MethodPost:
		body = strings.NewReader(params.Encode())
	default:
		return fmt.Errorf("binance: unsupported method %s", method)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, values := range headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("binance: %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("binance: reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("binance: %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("binance: decode response: %w", err)
	}
	return nil
}

func sign(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
