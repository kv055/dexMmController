package bitcoin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NetworkDataFetcher handles all Bitcoin network data API calls
type NetworkDataFetcher struct {
	client     *http.Client
	baseURL    string
	userAgent  string
	lastUpdate time.Time
}

// NewNetworkDataFetcher creates a new fetcher with mempool.space as default
func NewNetworkDataFetcher() *NetworkDataFetcher {
	return &NetworkDataFetcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:   "https://mempool.space/api/v1",
		userAgent: "dexMmController/1.0 (Bitcoin Fee Predictor)",
	}
}

// FetchRecommendedFees fetches current recommended fees from mempool.space
func (ndf *NetworkDataFetcher) FetchRecommendedFees() (*FeeEstimate, error) {
	url := ndf.baseURL + "/fees/recommended"

	resp, err := ndf.makeRequest(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recommended fees: %w", err)
	}
	defer resp.Body.Close()

	var feeResp BitcoinFeeResponse
	if err := json.NewDecoder(resp.Body).Decode(&feeResp); err != nil {
		return nil, fmt.Errorf("failed to decode fee response: %w", err)
	}

	// Map API response to our fee estimate structure
	estimate := &FeeEstimate{
		EconomicFee:  feeResp.EconomyFee,
		StandardFee:  feeResp.HalfHourFee,
		FastFee:      feeResp.FastestFee,
		ExpressFee:   feeResp.FastestFee + 5, // Add buffer for express tier
		MinimumFee:   feeResp.MinimumFee,
		LastUpdated:  time.Now(),
		Confidence:   0.95, // High confidence from primary source
		Source:       "mempool.space",
	}

	ndf.lastUpdate = time.Now()
	return estimate, nil
}

// FetchMempoolData fetches current mempool statistics
func (ndf *NetworkDataFetcher) FetchMempoolData() (*MempoolData, error) {
	url := ndf.baseURL + "/mempool"

	resp, err := ndf.makeRequest(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch mempool data: %w", err)
	}
	defer resp.Body.Close()

	var mempoolResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&mempoolResp); err != nil {
		return nil, fmt.Errorf("failed to decode mempool response: %w", err)
	}

	// Parse mempool response
	data := &MempoolData{
		BlockTarget:        make(map[string]int64),
		PercentileFeeRates: make(map[string]int64),
		FetchedAt:          time.Now(),
	}

	// Extract relevant fields
	if count, ok := mempoolResp["count"].(float64); ok {
		data.TotalUnconfirmedTxs = int64(count)
	}
	if memUsed, ok := mempoolResp["memUsed"].(float64); ok {
		data.TotalMemoryUsed = int64(memUsed)
	}
	if vSizes, ok := mempoolResp["vSizes"].([]interface{}); ok && len(vSizes) > 0 {
		// Extract fee rate statistics
		if vSize := vSizes[0].(map[string]interface{}); ok {
			if avg, ok := vSize["avgFee"].(float64); ok {
				data.AverageFeeRate = int64(avg)
			}
		}
	}

	return data, nil
}

// FetchBlockHeight fetches current block height
func (ndf *NetworkDataFetcher) FetchBlockHeight() (int32, error) {
	url := ndf.baseURL + "/blocks/tip/height"

	resp, err := ndf.makeRequest(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch block height: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	var height int32
	if err := json.Unmarshal(body, &height); err != nil {
		return 0, fmt.Errorf("failed to parse block height: %w", err)
	}

	return height, nil
}

// FetchFeePercentiles fetches fee rate percentiles for mempool
func (ndf *NetworkDataFetcher) FetchFeePercentiles() (map[string]int64, error) {
	url := ndf.baseURL + "/fees/mempool-blocks"

	resp, err := ndf.makeRequest(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch fee percentiles: %w", err)
	}
	defer resp.Body.Close()

	var blocks []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&blocks); err != nil {
		return nil, fmt.Errorf("failed to decode fee percentiles: %w", err)
	}

	percentiles := make(map[string]int64)

	// Extract feeRange from first block (next 10 minutes)
	if len(blocks) > 0 && blocks[0] != nil {
		if feeRange, ok := blocks[0]["feeRange"].([]interface{}); ok && len(feeRange) >= 2 {
			if minFee, ok := feeRange[0].(float64); ok {
				percentiles["min"] = int64(minFee)
			}
			if maxFee, ok := feeRange[len(feeRange)-1].(float64); ok {
				percentiles["max"] = int64(maxFee)
			}
		}
	}

	// Extract median from middle blocks
	if len(blocks) > 1 && blocks[1] != nil {
		if feeRange, ok := blocks[1]["feeRange"].([]interface{}); ok && len(feeRange) > 0 {
			if medianFee, ok := feeRange[len(feeRange)/2].(float64); ok {
				percentiles["median"] = int64(medianFee)
			}
		}
	}

	return percentiles, nil
}

// FetchNetworkStats fetches comprehensive network statistics
func (ndf *NetworkDataFetcher) FetchNetworkStats() (*NetworkStats, error) {
	fees, err := ndf.FetchRecommendedFees()
	if err != nil {
		return nil, err
	}

	mempool, err := ndf.FetchMempoolData()
	if err != nil {
		return nil, err
	}

	height, err := ndf.FetchBlockHeight()
	if err != nil {
		return nil, err
	}

	// Determine congestion level based on fee ratios
	congestion := "low"
	if fees.StandardFee > 10 && fees.FastFee > 20 {
		congestion = "high"
	} else if fees.StandardFee > 5 || fees.FastFee > 10 {
		congestion = "medium"
	}

	stats := &NetworkStats{
		MempoolSize:      mempool.TotalMemoryUsed,
		TransactionCount: mempool.TotalUnconfirmedTxs,
		AverageBlockTime: 10 * time.Minute, // Bitcoin standard
		CongestionLevel:  congestion,
		LastBlockHeight:  height,
		LastBlockTime:    time.Now(),
		TotalFeesInMempool: 0, // Can be calculated from mempool data if available
	}

	return stats, nil
}

// makeRequest performs an HTTP GET request with proper headers
func (ndf *NetworkDataFetcher) makeRequest(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", ndf.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := ndf.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	return resp, nil
}

// addressResponse is the relevant subset of the mempool.space GET /api/address/:address response.
type addressResponse struct {
	ChainStats struct {
		FundedSum int64 `json:"funded_txo_sum"` // satoshis received (confirmed)
		SpentSum  int64 `json:"spent_txo_sum"`  // satoshis spent (confirmed)
	} `json:"chain_stats"`
}

// FetchAddressBalance returns the confirmed BTC balance of a Bitcoin address
// by querying the mempool.space public API.
// The returned value is in BTC (not satoshis).
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

	var ar addressResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return 0, fmt.Errorf("bitcoin: failed to decode address response: %w", err)
	}

	confirmedSatoshis := ar.ChainStats.FundedSum - ar.ChainStats.SpentSum
	return float64(confirmedSatoshis) / 1e8, nil
}

// GetLastUpdate returns when data was last fetched
func (ndf *NetworkDataFetcher) GetLastUpdate() time.Time {
	return ndf.lastUpdate
}
