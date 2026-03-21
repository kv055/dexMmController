package hodlhodl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
		return fmt.Errorf("hodlhodl: %s %s returned HTTP %d", req.Method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}

	// Decode into a raw envelope first to check the status field.
	var envelope struct {
		Status           string              `json:"status"`
		ErrorCode        string              `json:"error_code,omitempty"`
		ValidationErrors map[string][]string `json:"validation_errors,omitempty"`
	}
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
