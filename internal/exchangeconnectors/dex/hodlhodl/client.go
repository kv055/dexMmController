package hodlhodl

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://hodlhodl.com"

// Client is a REST client for the HodlHodl API v1.
// Auth uses the Authorization: Bearer <api_key> header.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// MeResponse represents a me response.
type MeResponse struct {
	// User is the user value for the me response.
	User json.RawMessage `json:"user"`
}

// NewClient creates a new HodlHodl API client.
// baseURL should be e.g. "https://hodlhodl.com" (no trailing slash).
// Pass an empty baseURL to use the default.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// errorEnvelope is the common error shape returned by the HodlHodl API.
type errorEnvelope struct {
	Status           string              `json:"status"`
	ErrorCode        string              `json:"error_code,omitempty"`
	ValidationErrors map[string][]string `json:"validation_errors,omitempty"`
}

// detail flattens the envelope into a single human-readable line.
func (e errorEnvelope) detail() string {
	parts := make([]string, 0, 1+len(e.ValidationErrors))
	if e.ErrorCode != "" {
		parts = append(parts, e.ErrorCode)
	}
	fields := make([]string, 0, len(e.ValidationErrors))
	for field := range e.ValidationErrors {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s %s", field, strings.Join(e.ValidationErrors[field], ", ")))
	}
	return strings.Join(parts, ": ")
}

// readErrorDetail extracts the API error description from an error response
// body, returning "" when the body is not a recognizable error envelope.
func readErrorDetail(body io.Reader) string {
	var envelope errorEnvelope
	if err := json.NewDecoder(io.LimitReader(body, 4096)).Decode(&envelope); err != nil {
		return ""
	}
	return envelope.detail()
}

// get performs an authenticated GET and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.do(req, path, out)
}

func (c *Client) do(req *http.Request, path string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hodlhodl: %s %s failed: %w", req.Method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("hodlhodl: %s %s rate limited (HTTP 429)", req.Method, path)
	}
	if resp.StatusCode != http.StatusOK {
		if detail := readErrorDetail(resp.Body); detail != "" {
			return fmt.Errorf("hodlhodl: %s %s returned HTTP %d (%s)", req.Method, path, resp.StatusCode, detail)
		}
		return fmt.Errorf("hodlhodl: %s %s returned HTTP %d", req.Method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}

	// Decode into a raw envelope first to check the status field.
	var envelope errorEnvelope
	// We need to decode twice: once for the envelope, once for the typed response.
	// Use a json.RawMessage intermediary.
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("hodlhodl: %s %s decode failed: %w", req.Method, path, err)
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("hodlhodl: %s %s envelope decode failed: %w", req.Method, path, err)
	}
	if envelope.Status == "error" {
		return fmt.Errorf("hodlhodl: %s %s API error: %s", req.Method, path, envelope.ErrorCode)
	}

	return json.Unmarshal(raw, out)
}

// post performs an authenticated POST with a JSON body and decodes the response into out.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("hodlhodl: encode body: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.do(req, path, out)
}

// ---------- Offers ----------

// GetOffer fetches a single offer by ID. Does not require authentication.
func (c *Client) GetOffer(ctx context.Context, offerID string) (*Offer, error) {
	var resp offerResponse
	if err := c.get(ctx, "/api/v1/offers/"+offerID, &resp); err != nil {
		return nil, err
	}
	return &resp.Offer, nil
}

// GetMyOffers lists the authenticated user's own offers.
func (c *Client) GetMyOffers(ctx context.Context) ([]Offer, error) {
	var resp offersResponse
	if err := c.get(ctx, "/api/v1/offers/my", &resp); err != nil {
		return nil, err
	}
	return resp.Offers, nil
}

// GetMe retrieves a me.
func (c *Client) GetMe(ctx context.Context) (*MeResponse, error) {
	var resp MeResponse
	if err := c.get(ctx, "/api/v1/users/me", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// serverTime returns the API server's clock read from the Date response
// header of a cheap HEAD request. Signed requests must carry a nonce within a
// tight window around the server's time, so the local clock cannot be trusted.
func (c *Client) serverTime(ctx context.Context) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL+"/api/v1/users/me", nil)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	return http.ParseTime(resp.Header.Get("Date"))
}

// VerifySignatureKey verifies that a HodlHodl API signature key is usable.
func (c *Client) VerifySignatureKey(ctx context.Context, signatureKey string) error {
	now := time.Now()
	if serverNow, err := c.serverTime(ctx); err == nil {
		now = serverNow
	}
	// Back-date the nonce slightly: the server rejects nonces ahead of its
	// clock, and the Date header has whole-second precision.
	nonce := strconv.FormatInt(now.Add(-2*time.Second).Unix(), 10)
	mac := hmac.New(sha256.New, []byte(signatureKey))
	_, _ = mac.Write([]byte(c.apiKey + ":" + nonce))
	body := map[string]string{
		"nonce": nonce,
		"hmac":  hex.EncodeToString(mac.Sum(nil)),
	}
	var resp map[string]any
	return c.post(ctx, "/api/v1/contracts/verify_signature_key", body, &resp)
}

// SearchOffers searches the public offer book with the given filters.
func (c *Client) SearchOffers(ctx context.Context, f OfferSearchFilters) ([]Offer, error) {
	path := "/api/v1/offers?"
	q := ""
	add := func(k, v string) {
		if v == "" {
			return
		}
		if q != "" {
			q += "&"
		}
		q += k + "=" + v
	}
	add("filters[side]", f.Side)
	add("filters[currency_code]", f.CurrencyCode)
	add("filters[asset_code]", f.AssetCode)
	add("filters[country]", f.Country)
	add("filters[amount]", f.Amount)
	if f.Limit > 0 {
		add("pagination[limit]", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		add("pagination[offset]", strconv.Itoa(f.Offset))
	}
	var resp offersResponse
	if err := c.get(ctx, path+q, &resp); err != nil {
		return nil, err
	}
	return resp.Offers, nil
}

// ---------- Contracts ----------

// GetContract fetches a single contract by ID. Requires authentication.
func (c *Client) GetContract(ctx context.Context, contractID string) (*Contract, error) {
	var resp contractResponse
	if err := c.get(ctx, "/api/v1/contracts/"+contractID, &resp); err != nil {
		return nil, err
	}
	return &resp.Contract, nil
}

// GetMyContracts lists the authenticated user's contracts, optionally filtered by status.
func (c *Client) GetMyContracts(ctx context.Context, status ContractStatus) ([]Contract, error) {
	path := "/api/v1/contracts/my"
	if status != "" {
		path += "?filters[status]=" + string(status)
	}
	var resp contractsResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Contracts, nil
}

// CreateContract accepts an offer by creating a new contract.
// Returns the newly created contract.
func (c *Client) CreateContract(ctx context.Context, p CreateContractParams) (*Contract, error) {
	body := map[string]any{
		"contract": map[string]any{
			"offer_id":                           p.OfferID,
			"offer_version":                      p.OfferVersion,
			"payment_method_instruction_id":      p.PaymentMethodInstructionID,
			"payment_method_instruction_version": p.PaymentMethodInstructionVer,
			"value":                              p.Value,
			"volume":                             p.Volume,
		},
	}
	if p.Comment != "" {
		body["contract"].(map[string]any)["comment"] = p.Comment
	}
	var resp contractResponse
	if err := c.post(ctx, "/api/v1/contracts", body, &resp); err != nil {
		return nil, err
	}
	return &resp.Contract, nil
}

// ---------- Payment method instructions ----------

// GetMyPaymentMethodInstructions lists the authenticated user's saved payment
// methods (bank accounts, etc.) that can be used when accepting offers.
func (c *Client) GetMyPaymentMethodInstructions(ctx context.Context) ([]PaymentMethodInstruction, error) {
	var resp paymentMethodInstructionsResponse
	if err := c.get(ctx, "/api/v1/payment_method_instructions", &resp); err != nil {
		return nil, err
	}
	return resp.PaymentMethodInstructions, nil
}

// ---------- Position taking ----------

// TakeOffer accepts an offer by ID with a fiat amount and returns the escrow
// bitcoin address to deposit BTC into. It fetches the offer version, looks up
// the first payment method instruction from your account, creates the contract,
// and polls until HodlHodl provisions the escrow address.
func (c *Client) TakeOffer(ctx context.Context, offerID string, fiatAmount string) (escrowAddress string, contract *Contract, err error) {
	offer, err := c.GetOffer(ctx, offerID)
	if err != nil {
		return "", nil, fmt.Errorf("TakeOffer: fetch offer: %w", err)
	}

	pmis, err := c.GetMyPaymentMethodInstructions(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("TakeOffer: fetch payment methods: %w", err)
	}
	if len(pmis) == 0 {
		return "", nil, fmt.Errorf("TakeOffer: no payment method instructions configured on your HodlHodl account")
	}
	pmi := pmis[0]

	contract, err = c.CreateContract(ctx, CreateContractParams{
		OfferID:                     offer.ID,
		OfferVersion:                offer.Version,
		PaymentMethodInstructionID:  pmi.ID,
		PaymentMethodInstructionVer: pmi.Version,
		Value:                       fiatAmount,
	})
	if err != nil {
		return "", nil, fmt.Errorf("TakeOffer: create contract: %w", err)
	}

	// Poll until the escrow address is provisioned by HodlHodl.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for contract.Escrow.Address == "" {
		select {
		case <-ctx.Done():
			return "", contract, fmt.Errorf("TakeOffer: context cancelled waiting for escrow address: %w", ctx.Err())
		case <-ticker.C:
			contract, err = c.GetContract(ctx, contract.ID)
			if err != nil {
				return "", nil, fmt.Errorf("TakeOffer: poll contract: %w", err)
			}
		}
	}

	return contract.Escrow.Address, contract, nil
}

// ---------- Healthcheck helpers ----------

// IsOfferLive returns true if the offer exists and has status "active".
func (c *Client) IsOfferLive(ctx context.Context, offerID string) (bool, error) {
	offer, err := c.GetOffer(ctx, offerID)
	if err != nil {
		return false, err
	}
	return offer.Status == "active", nil
}

// IsContractCompleted returns true if the contract has reached "completed" status,
// meaning the BTC has been released to the buyer.
func (c *Client) IsContractCompleted(ctx context.Context, contractID string) (bool, error) {
	contract, err := c.GetContract(ctx, contractID)
	if err != nil {
		return false, err
	}
	return contract.Status == ContractCompleted, nil
}

// ContractHasCounterparty returns true if the contract is past the "pending" stage,
// meaning a counterparty has accepted the trade.
func (c *Client) ContractHasCounterparty(ctx context.Context, contractID string) (bool, error) {
	contract, err := c.GetContract(ctx, contractID)
	if err != nil {
		return false, err
	}
	// Any status beyond "pending" means someone took the offer.
	return contract.Status != ContractPending, nil
}

// ContractIsPaid returns true if the buyer has marked the contract as paid
// (fiat sent), so the seller knows to check their bank account.
func (c *Client) ContractIsPaid(ctx context.Context, contractID string) (bool, error) {
	contract, err := c.GetContract(ctx, contractID)
	if err != nil {
		return false, err
	}
	return contract.Status == ContractPaid || contract.Status == ContractCompleted, nil
}
