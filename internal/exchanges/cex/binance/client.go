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
	"sync"
	"time"
)

const (
	defaultBaseURL      = "https://api.binance.com"
	defaultRecvWindowMS = int64(10000)
)

// Client represents a client.
type Client struct {
	baseURL      string
	apiKey       string
	secretKey    string
	httpClient   *http.Client
	timeMu       sync.Mutex
	timeOffsetMS int64
	timeSynced   bool
}

// DepositAddressResponse represents a deposit address response.
type DepositAddressResponse struct {
	// Address is the wallet or peer address used by the deposit address response.
	Address string `json:"address"`
	// Coin names the asset or network for the deposit address response.
	Coin string `json:"coin"`
	// Tag is the tag value for the deposit address response.
	Tag string `json:"tag"`
	// URL is the endpoint URL.
	URL string `json:"url"`
	// IsDefault reports whether the deposit address response condition is true.
	IsDefault int `json:"isDefault"`
}

// WithdrawRequest represents a withdraw request.
type WithdrawRequest struct {
	// Coin names the asset or network for the withdraw request.
	Coin string
	// Address is the wallet or peer address used by the withdraw request.
	Address string
	// Amount is the amount value for the withdraw request.
	Amount string
	// Network names the asset or network for the withdraw request.
	Network string
	// AddressTag is the wallet or peer address used by the withdraw request.
	AddressTag string
	// WithdrawOrderID preserves additional source data for the withdraw request.
	WithdrawOrderID string
	// TransactionFeeFlag is the fee value reported for the withdraw request.
	TransactionFeeFlag *bool
	// Name is the name value for the withdraw request.
	Name string
	// WalletType is the wallet type value for the withdraw request.
	WalletType *int
	// RecvWindow is the recv window value for the withdraw request.
	RecvWindow int64
}

// WithdrawResponse represents a withdraw response.
type WithdrawResponse struct {
	// ID identifies the withdraw response.
	ID string `json:"id"`
}

// DepositHistoryParams represents a deposit history params.
type DepositHistoryParams struct {
	// Coin names the asset or network for the deposit history params.
	Coin string
	// Status is the current status of the deposit history params.
	Status *int
	// StartTime records the relevant time for the deposit history params.
	StartTime *time.Time
	// EndTime records the relevant time for the deposit history params.
	EndTime *time.Time
	// Offset is the offset value for the deposit history params.
	Offset *int
	// Limit is the limit value for the deposit history params.
	Limit *int
	// RecvWindow is the recv window value for the deposit history params.
	RecvWindow int64
}

// DepositRecord represents a deposit record.
type DepositRecord struct {
	// ID identifies the deposit record.
	ID string `json:"id"`
	// Amount is the amount value for the deposit record.
	Amount string `json:"amount"`
	// Coin names the asset or network for the deposit record.
	Coin string `json:"coin"`
	// Network names the asset or network for the deposit record.
	Network string `json:"network"`
	// Status is the current status of the deposit record.
	Status int `json:"status"`
	// Address is the wallet or peer address used by the deposit record.
	Address string `json:"address"`
	// AddressTag is the wallet or peer address used by the deposit record.
	AddressTag string `json:"addressTag"`
	// TxID is the tx id value for the deposit record.
	TxID string `json:"txId"`
	// InsertTime records the relevant time for the deposit record.
	InsertTime int64 `json:"insertTime"`
	// TransferType is the transfer type value for the deposit record.
	TransferType int `json:"transferType"`
	// ConfirmTimes records the relevant time for the deposit record.
	ConfirmTimes string `json:"confirmTimes"`
	// UnlockConfirm is the unlock confirm value for the deposit record.
	UnlockConfirm string `json:"unlockConfirm"`
}

// WithdrawHistoryParams represents a withdraw history params.
type WithdrawHistoryParams struct {
	// Coin names the asset or network for the withdraw history params.
	Coin string
	// WithdrawOrderID preserves additional source data for the withdraw history params.
	WithdrawOrderID string
	// Status is the current status of the withdraw history params.
	Status *int
	// StartTime records the relevant time for the withdraw history params.
	StartTime *time.Time
	// EndTime records the relevant time for the withdraw history params.
	EndTime *time.Time
	// Offset is the offset value for the withdraw history params.
	Offset *int
	// Limit is the limit value for the withdraw history params.
	Limit *int
	// RecvWindow is the recv window value for the withdraw history params.
	RecvWindow int64
}

// WithdrawRecord represents a withdraw record.
type WithdrawRecord struct {
	// ID identifies the withdraw record.
	ID string `json:"id"`
	// Amount is the amount value for the withdraw record.
	Amount string `json:"amount"`
	// TransactionFee is the fee value reported for the withdraw record.
	TransactionFee string `json:"transactionFee"`
	// Coin names the asset or network for the withdraw record.
	Coin string `json:"coin"`
	// Status is the current status of the withdraw record.
	Status int `json:"status"`
	// Address is the wallet or peer address used by the withdraw record.
	Address string `json:"address"`
	// ApplyTime records the relevant time for the withdraw record.
	ApplyTime string `json:"applyTime"`
	// Network names the asset or network for the withdraw record.
	Network string `json:"network"`
	// TransferType is the transfer type value for the withdraw record.
	TransferType int `json:"transferType"`
	// WithdrawOrderID preserves additional source data for the withdraw record.
	WithdrawOrderID string `json:"withdrawOrderId"`
	// TxID is the tx id value for the withdraw record.
	TxID string `json:"txId"`
}

// SpotBalance is a single asset balance from the spot account endpoint.
type SpotBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// spotAccountResponse is the balance payload from GET /api/v3/account.
type spotAccountResponse struct {
	Balances []SpotBalance `json:"balances"`
}

// AccountInfo reports Binance account permissions and balances.
type AccountInfo struct {
	// CanTrade is the can trade value for the account info.
	CanTrade bool `json:"canTrade"`
	// CanWithdraw preserves additional source data for the account info.
	CanWithdraw bool `json:"canWithdraw"`
	// CanDeposit is the can deposit value for the account info.
	CanDeposit bool `json:"canDeposit"`
}

// KeyRestrictions reports API-key-level permissions from Binance.
type KeyRestrictions struct {
	// IPRestrict reports whether the key is IP-restricted.
	IPRestrict bool `json:"ipRestrict"`
	// EnableReading reports whether the key can read account data.
	EnableReading bool `json:"enableReading"`
	// EnableSpotAndMarginTrading reports whether the key can trade spot/margin.
	EnableSpotAndMarginTrading bool `json:"enableSpotAndMarginTrading"`
	// EnableWithdrawals reports whether the key can withdraw funds.
	EnableWithdrawals bool `json:"enableWithdrawals"`
	// EnableFutures reports whether the key can trade USDⓈ-M/COIN-M futures (hedging).
	EnableFutures bool `json:"enableFutures"`
}

// NewClient creates a client.
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

// Name returns the exchange or bridge name.
func (c *Client) Name() string {
	return "binance"
}

// AccountInfo retrieves account capability and permission details.
func (c *Client) AccountInfo(ctx context.Context) (*AccountInfo, error) {
	var resp AccountInfo
	if err := c.signedRequest(ctx, http.MethodGet, "/api/v3/account", nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SpotBalances retrieves non-zero spot wallet balances.
func (c *Client) SpotBalances(ctx context.Context) ([]SpotBalance, error) {
	var resp spotAccountResponse
	if err := c.signedRequest(ctx, http.MethodGet, "/api/v3/account", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

// KeyRestrictions retrieves API-key permission flags for the configured credentials.
func (c *Client) KeyRestrictions(ctx context.Context) (*KeyRestrictions, error) {
	var resp KeyRestrictions
	if err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/account/apiRestrictions", nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// WithdrawAddressEntry is a saved withdrawal address from the account address book.
type WithdrawAddressEntry struct {
	// Address is the wallet or peer address used by the withdraw address entry.
	Address string `json:"address"`
	// AddressTag is the optional tag/memo for the withdraw address entry.
	AddressTag string `json:"addressTag"`
	// Coin names the asset for the withdraw address entry.
	Coin string `json:"coin"`
	// Network names the blockchain network for the withdraw address entry.
	Network string `json:"network"`
	// Name is the user-defined label for the withdraw address entry.
	Name string `json:"name"`
	// WhiteStatus reports whether the address is whitelisted.
	WhiteStatus bool `json:"whiteStatus"`
}

// WithdrawAddressList retrieves the saved (address book) withdrawal addresses.
func (c *Client) WithdrawAddressList(ctx context.Context) ([]WithdrawAddressEntry, error) {
	var resp []WithdrawAddressEntry
	if err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/capital/withdraw/address/list", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetDepositAddress retrieves a deposit address.
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

// ApplyWithdraw submits a withdrawal request to Binance.
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

// OrderFill is a single fill (partial execution) within a Binance order response.
type OrderFill struct {
	// Price is the fill price, as a decimal string.
	Price string `json:"price"`
	// Qty is the filled base-asset quantity for this fill, as a decimal string.
	Qty string `json:"qty"`
	// Commission is the fee charged for this fill, as a decimal string.
	Commission string `json:"commission"`
	// CommissionAsset is the asset the fee was charged in (may differ from the
	// quote currency, e.g. "BNB" when the fee-discount program is active).
	CommissionAsset string `json:"commissionAsset"`
	// TradeID is the exchange-assigned trade id for this fill.
	TradeID int64 `json:"tradeId"`
}

// OrderResponse is the result of a Binance spot order placement.
type OrderResponse struct {
	// Symbol is the trading symbol the order was placed on.
	Symbol string `json:"symbol"`
	// OrderID is the exchange-assigned order id.
	OrderID int64 `json:"orderId"`
	// Status is the current order status (e.g. FILLED).
	Status string `json:"status"`
	// ExecutedQty is the filled base-asset quantity.
	ExecutedQty string `json:"executedQty"`
	// CummulativeQuoteQty is the filled quote-asset amount (spelling per Binance API).
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	// Fills lists the individual executions that made up this order. Present for
	// MARKET orders, which use Binance's default FULL response type.
	Fills []OrderFill `json:"fills"`
}

// MarketBuy places a market buy order for quantityBTC of the base asset on symbol (e.g. "BTCEUR").
func (c *Client) MarketBuy(ctx context.Context, symbol string, quantityBTC float64) (*OrderResponse, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "MARKET")
	params.Set("quantity", strconv.FormatFloat(quantityBTC, 'f', -1, 64))

	var resp OrderResponse
	if err := c.signedRequest(ctx, http.MethodPost, "/api/v3/order", params, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDepositHistory retrieves a deposit history.
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

// GetWithdrawHistory retrieves a withdraw history.
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
	err := c.doSignedRequest(ctx, method, path, params, headers, out)
	if err == nil || !isBinanceTimestampError(err) {
		return err
	}
	c.invalidateServerTime()
	return c.doSignedRequest(ctx, method, path, params, headers, out)
}

func (c *Client) doSignedRequest(ctx context.Context, method, path string, params url.Values, headers http.Header, out any) error {
	if params == nil {
		params = url.Values{}
	}
	timestamp, err := c.serverTimestamp(ctx)
	if err != nil {
		return err
	}
	if params.Get("recvWindow") == "" {
		params.Set("recvWindow", strconv.FormatInt(defaultRecvWindowMS, 10))
	}
	params.Set("timestamp", strconv.FormatInt(timestamp, 10))
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

func (c *Client) serverTimestamp(ctx context.Context) (int64, error) {
	c.timeMu.Lock()
	defer c.timeMu.Unlock()
	if !c.timeSynced {
		if err := c.syncServerTimeLocked(ctx); err != nil {
			return 0, err
		}
	}
	return time.Now().UnixMilli() + c.timeOffsetMS, nil
}

func (c *Client) syncServerTimeLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v3/time", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("binance: GET /api/v3/time failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("binance: reading server time: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("binance: GET /api/v3/time returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("binance: decode server time: %w", err)
	}
	c.timeOffsetMS = payload.ServerTime - time.Now().UnixMilli()
	c.timeSynced = true
	return nil
}

func (c *Client) invalidateServerTime() {
	c.timeMu.Lock()
	c.timeSynced = false
	c.timeOffsetMS = 0
	c.timeMu.Unlock()
}

func isBinanceTimestampError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, `"code":-1021`) || strings.Contains(msg, "Timestamp for this request")
}

func sign(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
