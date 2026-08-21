package magma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const defaultOffersEndpoint = "https://api.amboss.space/graphql"

// Offer is one listing in the Amboss Magma marketplace offer book.
type Offer struct {
	ID             string `json:"id"`
	Side           string `json:"side"`
	Status         string `json:"status"`
	BaseFee        int64  `json:"base_fee"`
	FeeRate        int64  `json:"fee_rate"`
	BaseFeeCap     *int64 `json:"base_fee_cap,omitempty"`
	FeeRateCap     *int64 `json:"fee_rate_cap,omitempty"`
	MinSizeSat     int64  `json:"min_size_sat"`
	MaxSizeSat     int64  `json:"max_size_sat"`
	TotalSizeSat   int64  `json:"total_size_sat"`
	MinBlockLength int64  `json:"min_block_length"`
	SellerScore    string `json:"seller_score"`
	SellerPubkey   string `json:"seller_pubkey"`
}

type offersGraphQLResponse struct {
	Data struct {
		GetOffers struct {
			List []struct {
				ID             string `json:"id"`
				Side           string `json:"side"`
				Status         string `json:"status"`
				BaseFee        int64  `json:"base_fee"`
				FeeRate        int64  `json:"fee_rate"`
				BaseFeeCap     *int64 `json:"base_fee_cap"`
				FeeRateCap     *int64 `json:"fee_rate_cap"`
				MinSize        string `json:"min_size"`
				MaxSize        string `json:"max_size"`
				TotalSize      string `json:"total_size"`
				MinBlockLength int64  `json:"min_block_length"`
				SellerScore    string `json:"seller_score"`
				Account        string `json:"account"`
			} `json:"list"`
		} `json:"getOffers"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// OffersClient queries the Amboss GraphQL API for marketplace offers.
type OffersClient struct {
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
}

// NewOffersClient returns an offers client using the default Amboss endpoint when empty.
func NewOffersClient(endpoint, apiKey string) *OffersClient {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		endpoint = defaultOffersEndpoint
	}
	return &OffersClient{
		Endpoint:   endpoint,
		APIKey:     strings.TrimSpace(apiKey),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

const offersQuery = `query { getOffers { list { id side status base_fee fee_rate base_fee_cap fee_rate_cap min_size max_size total_size min_block_length seller_score account } } }`

// ListOffers returns all ENABLED SELL offers sorted by cheapest routing fee.
func (c *OffersClient) ListOffers(ctx context.Context) ([]Offer, error) {
	if c == nil {
		return nil, fmt.Errorf("magma offers client is nil")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	body, err := json.Marshal(map[string]string{"query": offersQuery})
	if err != nil {
		return nil, fmt.Errorf("encode magma offers query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("magma offers request failed: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read magma offers response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("magma offers HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var parsed offersGraphQLResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, fmt.Errorf("decode magma offers response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("magma offers query failed: %s", parsed.Errors[0].Message)
	}
	offers := make([]Offer, 0, len(parsed.Data.GetOffers.List))
	for _, raw := range parsed.Data.GetOffers.List {
		if !strings.EqualFold(raw.Side, "SELL") || !strings.EqualFold(raw.Status, "ENABLED") {
			continue
		}
		minSize, err := ParseSatString(raw.MinSize)
		if err != nil {
			continue
		}
		maxSize, err := ParseSatString(raw.MaxSize)
		if err != nil {
			continue
		}
		totalSize, _ := ParseSatString(raw.TotalSize)
		offers = append(offers, Offer{
			ID:             raw.ID,
			Side:           raw.Side,
			Status:         raw.Status,
			BaseFee:        raw.BaseFee,
			FeeRate:        raw.FeeRate,
			BaseFeeCap:     raw.BaseFeeCap,
			FeeRateCap:     raw.FeeRateCap,
			MinSizeSat:     minSize,
			MaxSizeSat:     maxSize,
			TotalSizeSat:   totalSize,
			MinBlockLength: raw.MinBlockLength,
			SellerScore:    raw.SellerScore,
			SellerPubkey:   raw.Account,
		})
	}
	SortOffers(offers)
	return offers, nil
}

// SortOffers orders offers by cheapest routing fee, then base fee.
func SortOffers(offers []Offer) {
	sort.SliceStable(offers, func(i, j int) bool {
		if offers[i].FeeRate != offers[j].FeeRate {
			return offers[i].FeeRate < offers[j].FeeRate
		}
		return offers[i].BaseFee < offers[j].BaseFee
	})
}
