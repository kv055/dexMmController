package kraken

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	sharedkraken "commons/cexclient/kraken"
)

const defaultBaseURL = "https://api.kraken.com"

// Client represents a client.
type Client struct {
	sdk *sharedkraken.Client
}

// DepositMethod represents a deposit method.
type DepositMethod struct {
	// Method is the method value for the deposit method.
	Method string `json:"method"`
	// Limit is the limit value for the deposit method.
	Limit bool `json:"limit"`
	// GenAddress is the wallet or peer address used by the deposit method.
	GenAddress bool `json:"gen-address"`
}

// DepositAddress represents a deposit address.
type DepositAddress struct {
	// Address is the wallet or peer address used by the deposit address.
	Address string `json:"address"`
	// ExpireTM is the expire tm value for the deposit address.
	ExpireTM string `json:"expiretm"`
	// New is the new value for the deposit address.
	New bool `json:"new,omitempty"`
}

// DepositAddressRequest represents a deposit address request.
type DepositAddressRequest struct {
	// Asset is the asset value for the deposit address request.
	Asset string
	// Method is the method value for the deposit address request.
	Method string
	// CreateNew is the create new value for the deposit address request.
	CreateNew bool
	// Amount is the amount value for the deposit address request.
	Amount string
}

// WithdrawRequest represents a withdraw request.
type WithdrawRequest struct {
	// Asset is the asset value for the withdraw request.
	Asset string
	// Key is the key value for the withdraw request.
	Key string
	// Address is the wallet or peer address used by the withdraw request.
	Address string
	// Method is the method value for the withdraw request.
	Method string
	// Amount is the amount value for the withdraw request.
	Amount string
}

// WithdrawMethod represents a withdraw method.
type WithdrawMethod struct {
	// Method is the method value for the withdraw method.
	Method string `json:"method"`
	// Limit is the limit value for the withdraw method.
	Limit bool `json:"limit"`
	// Fee is the fee reported for the withdraw method. Kraken returns this as either
	// a string or a nested object depending on asset/method, so keep it raw.
	Fee json.RawMessage `json:"fee"`
}

// WithdrawResponse represents a withdraw response.
type WithdrawResponse struct {
	// RefID is the ref id value for the withdraw response.
	RefID string `json:"refid"`
}

// WithdrawAddress is a saved (address book) withdrawal address.
type WithdrawAddress struct {
	// Address is the wallet or peer address used by the withdraw address.
	Address string `json:"address"`
	// Asset is the asset value for the withdraw address.
	Asset string `json:"asset"`
	// Method is the withdrawal method name for the withdraw address.
	Method string `json:"method"`
	// Key is the withdrawal key (label) configured on the account.
	Key string `json:"key"`
	// Tag is the optional tag/memo for the withdraw address.
	Tag string `json:"tag"`
	// Verified reports whether the address completed email confirmation.
	Verified bool `json:"verified"`
}

// DepositStatusRequest represents a deposit status request.
type DepositStatusRequest struct {
	// Asset is the asset value for the deposit status request.
	Asset string
	// Method is the method value for the deposit status request.
	Method string
	// StartTime records the relevant time for the deposit status request.
	StartTime *time.Time
	// EndTime records the relevant time for the deposit status request.
	EndTime *time.Time
}

// DepositStatusRecord represents a deposit status record.
type DepositStatusRecord struct {
	// Method is the method value for the deposit status record.
	Method string `json:"method"`
	// Asset is the asset value for the deposit status record.
	Asset string `json:"asset"`
	// RefID is the ref id value for the deposit status record.
	RefID string `json:"refid"`
	// TxID is the tx id value for the deposit status record.
	TxID string `json:"txid"`
	// Info is the info value for the deposit status record.
	Info string `json:"info"`
	// Amount is the amount value for the deposit status record.
	Amount string `json:"amount"`
	// Fee is the fee value reported for the deposit status record.
	Fee string `json:"fee"`
	// Time records the relevant time for the deposit status record.
	Time int64 `json:"time"`
	// Status is the current status of the deposit status record.
	Status string `json:"status"`
}

// WithdrawStatusRequest represents a withdraw status request.
type WithdrawStatusRequest struct {
	// Asset is the asset value for the withdraw status request.
	Asset string
	// Method is the method value for the withdraw status request.
	Method string
	// StartTime records the relevant time for the withdraw status request.
	StartTime *time.Time
	// EndTime records the relevant time for the withdraw status request.
	EndTime *time.Time
}

// WithdrawStatusRecord represents a withdraw status record.
type WithdrawStatusRecord struct {
	// Method is the method value for the withdraw status record.
	Method string `json:"method"`
	// Asset is the asset value for the withdraw status record.
	Asset string `json:"asset"`
	// RefID is the ref id value for the withdraw status record.
	RefID string `json:"refid"`
	// TxID is the tx id value for the withdraw status record.
	TxID string `json:"txid"`
	// Info is the info value for the withdraw status record.
	Info string `json:"info"`
	// Amount is the amount value for the withdraw status record.
	Amount string `json:"amount"`
	// Fee is the fee value reported for the withdraw status record.
	Fee string `json:"fee"`
	// Time records the relevant time for the withdraw status record.
	Time int64 `json:"time"`
	// Status is the current status of the withdraw status record.
	Status string `json:"status"`
}

// Balance identifies a balance.
type Balance map[string]string

type apiEnvelope[T any] struct {
	Error  []string `json:"error"`
	Result T        `json:"result"`
}

// NewClient creates a client.
func NewClient(baseURL, apiKey, secretKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		sdk: sharedkraken.New(apiKey, secretKey, sharedkraken.Options{BaseURL: baseURL}),
	}
}

// Name returns the exchange or bridge name.
func (c *Client) Name() string {
	return "kraken"
}

// Balance retrieves balances from the exchange.
func (c *Client) Balance(ctx context.Context) (Balance, error) {
	var resp apiEnvelope[Balance]
	if err := c.privateRequest(ctx, "/0/private/Balance", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// GetDepositMethods retrieves a deposit methods.
func (c *Client) GetDepositMethods(ctx context.Context, asset string) ([]DepositMethod, error) {
	form := url.Values{}
	form.Set("asset", asset)

	var resp apiEnvelope[[]DepositMethod]
	if err := c.privateRequest(ctx, "/0/private/DepositMethods", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// GetDepositAddresses retrieves a deposit addresses.
func (c *Client) GetDepositAddresses(ctx context.Context, asset, method string, createNew bool) ([]DepositAddress, error) {
	return c.GetDepositAddressesWithOptions(ctx, DepositAddressRequest{
		Asset:     asset,
		Method:    method,
		CreateNew: createNew,
	})
}

// GetDepositAddressesWithOptions retrieves a deposit addresses with options.
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

// GetWithdrawMethods retrieves a withdraw methods.
func (c *Client) GetWithdrawMethods(ctx context.Context, asset string) ([]WithdrawMethod, error) {
	form := url.Values{}
	form.Set("asset", asset)

	var resp apiEnvelope[[]WithdrawMethod]
	if err := c.privateRequest(ctx, "/0/private/WithdrawMethods", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// GetWithdrawAddresses retrieves the saved (address book) withdrawal addresses.
func (c *Client) GetWithdrawAddresses(ctx context.Context, asset string) ([]WithdrawAddress, error) {
	form := url.Values{}
	if asset != "" {
		form.Set("asset", asset)
	}

	var resp apiEnvelope[[]WithdrawAddress]
	if err := c.privateRequest(ctx, "/0/private/WithdrawAddresses", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// Withdraw withdraws funds using the configured bridge.
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

// GetDepositStatus retrieves a deposit status.
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

// GetWithdrawStatus retrieves a withdraw status.
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

// AddOrderResult is the result of a Kraken AddOrder call.
type AddOrderResult struct {
	// Descr describes the submitted order.
	Descr struct {
		// Order is the human-readable order description.
		Order string `json:"order"`
	} `json:"descr"`
	// TxID lists the transaction ids created for the order.
	TxID []string `json:"txid"`
}

// MarketBuy places a market buy order for volumeBTC of the base asset on pair (e.g. "XBTEUR").
func (c *Client) MarketBuy(ctx context.Context, pair string, volumeBTC float64) (*AddOrderResult, error) {
	form := url.Values{}
	form.Set("pair", pair)
	form.Set("type", "buy")
	form.Set("ordertype", "market")
	form.Set("volume", strconv.FormatFloat(volumeBTC, 'f', -1, 64))

	var resp apiEnvelope[AddOrderResult]
	if err := c.privateRequest(ctx, "/0/private/AddOrder", form, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

// OrderInfo is a single order's status/fill details, as returned by QueryOrders.
type OrderInfo struct {
	// Status is the order status: "pending", "open", "closed", "canceled", "expired".
	Status string `json:"status"`
	// Vol is the requested order volume (base asset, BTC), as a decimal string.
	Vol string `json:"vol"`
	// VolExec is the executed volume (base asset, BTC), as a decimal string.
	VolExec string `json:"vol_exec"`
	// Cost is the total quote-currency cost of the fill, as a decimal string.
	Cost string `json:"cost"`
	// Fee is the total fee charged, in quote currency, as a decimal string.
	Fee string `json:"fee"`
	// Price is the volume-weighted average fill price, as a decimal string.
	Price string `json:"price"`
}

// QueryOrders retrieves order status/fill details for the given transaction id.
// This is the same endpoint Kraken uses for both still-open and recently-closed
// orders (closed orders remain queryable here for roughly 50 hours), so polling
// the tx id returned by AddOrder until its status flips to "closed" is enough to
// retrieve the executed price/cost/fee for a market order just placed.
func (c *Client) QueryOrders(ctx context.Context, txID string) (map[string]OrderInfo, error) {
	form := url.Values{}
	form.Set("txid", txID)

	var resp apiEnvelope[map[string]OrderInfo]
	if err := c.privateRequest(ctx, "/0/private/QueryOrders", form, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// ValidateOrderPermission probes whether the key may place orders by submitting an
// AddOrder with validate=true. Kraken validates the order WITHOUT placing it, so no
// funds are spent. A nil error means the key has order-creation permission; a
// "Permission denied" error means it does not (callers should classify it as denied).
func (c *Client) ValidateOrderPermission(ctx context.Context, pair string) error {
	form := url.Values{}
	form.Set("pair", pair)
	form.Set("type", "buy")
	form.Set("ordertype", "market")
	form.Set("volume", "0.0001")
	form.Set("validate", "true")

	var resp apiEnvelope[AddOrderResult]
	return c.privateRequest(ctx, "/0/private/AddOrder", form, &resp)
}

func (c *Client) privateRequest(ctx context.Context, path string, form url.Values, out any) error {
	body := make(map[string]any, len(form))
	for key, values := range form {
		if len(values) != 0 {
			body[key] = values[len(values)-1]
		}
	}
	raw, err := c.sdk.PrivatePOST(ctx, path, path, body)
	if err != nil {
		return err
	}
	// Kraken often returns result as {} or null alongside errors; check the error
	// array before unmarshaling into a typed result so Invalid key / Permission denied
	// are not lost as decode failures.
	var probe struct {
		Error []string `json:"error"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && len(probe.Error) > 0 {
		return fmt.Errorf("kraken: %s: %s", path, strings.Join(probe.Error, ", "))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("kraken: decode response: %w", err)
	}
	return krakenAPIError(path, out)
}

func krakenAPIError(path string, out any) error {
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
	case *apiEnvelope[[]WithdrawAddress]:
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
	case *apiEnvelope[Balance]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[AddOrderResult]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	case *apiEnvelope[map[string]OrderInfo]:
		if len(envelope.Error) > 0 {
			return fmt.Errorf("kraken: %s: %s", path, strings.Join(envelope.Error, ", "))
		}
	}
	return nil
}
