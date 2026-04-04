package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// NetworkDataFetcher handles Bitcoin network data API calls via mempool.space.
type NetworkDataFetcher struct {
	client    *http.Client
	userAgent string
}

// NewNetworkDataFetcher creates a new fetcher with mempool.space as the default source.
func NewNetworkDataFetcher() *NetworkDataFetcher {
	return &NetworkDataFetcher{
		client:    &http.Client{Timeout: 10 * time.Second},
		userAgent: "dexMmController/1.0",
	}
}

// FetchAddressBalance returns the confirmed BTC balance of a Bitcoin address
// by querying the mempool.space public API. The returned value is in BTC (not satoshis).
func (ndf *NetworkDataFetcher) FetchAddressBalance(ctx context.Context, address string) (float64, error) {
	url := "https://mempool.space/api/address/" + address

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("bitcoin: failed to create address request: %w", err)
	}
	req.Header.Set("User-Agent", ndf.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := ndf.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitcoin: address balance request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bitcoin: mempool.space returned HTTP %d for address %s", resp.StatusCode, address)
	}

	var ar struct {
		ChainStats struct {
			FundedSum int64 `json:"funded_txo_sum"`
			SpentSum  int64 `json:"spent_txo_sum"`
		} `json:"chain_stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return 0, fmt.Errorf("bitcoin: failed to decode address response: %w", err)
	}

	confirmedSatoshis := ar.ChainStats.FundedSum - ar.ChainStats.SpentSum
	return float64(confirmedSatoshis) / 1e8, nil
}
