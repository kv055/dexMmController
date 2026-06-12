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
	"sync"
	"time"
)

const defaultBaseURL = "https://api.kraken.com"

// Client represents a client.
type Client struct {
	baseURL    string
	apiKey     string
	secretKey  string
	httpClient *http.Client
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
	// Fee is the fee value reported for the withdraw method.
	Fee string `json:"fee"`
}

// WithdrawResponse represents a withdraw response.
type WithdrawResponse struct {
	// RefID is the ref id value for the withdraw response.
	RefID string `json:"refid"`
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
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 20 * time.Second},
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

type nonceSequencer struct {
	mu   sync.Mutex
	last int64
}

var apiNonceSequencers sync.Map

func (c *Client) privateRequest(ctx context.Context, path string, form url.Values, out any) error {
	err := c.doPrivateRequest(ctx, path, form, out)
	if err == nil || !isKrakenInvalidNonce(err) {
		return err
	}
	c.bumpNonceAhead()
	return c.doPrivateRequest(ctx, path, form, out)
}

func (c *Client) doPrivateRequest(ctx context.Context, path string, form url.Values, out any) error {
	if form == nil {
		form = url.Values{}
	}
	nonce := c.nextNonce()
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
	return krakenAPIError(path, out)
}

func (c *Client) nextNonce() string {
	value, _ := apiNonceSequencers.LoadOrStore(c.apiKey, &nonceSequencer{})
	seq := value.(*nonceSequencer)
	seq.mu.Lock()
	defer seq.mu.Unlock()
	now := time.Now().UnixMicro()
	if now <= seq.last {
		seq.last++
	} else {
		seq.last = now
	}
	return strconv.FormatInt(seq.last, 10)
}

func (c *Client) bumpNonceAhead() {
	value, _ := apiNonceSequencers.LoadOrStore(c.apiKey, &nonceSequencer{})
	seq := value.(*nonceSequencer)
	seq.mu.Lock()
	defer seq.mu.Unlock()
	now := time.Now().UnixMicro()
	if now > seq.last {
		seq.last = now
	}
	seq.last += 1000
}

func isKrakenInvalidNonce(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "EAPI:Invalid nonce")
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
