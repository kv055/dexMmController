package bitcoin

import (
	"fmt"
	"time"
)

// BitcoinFeePredictor orchestrates all fee prediction components
type BitcoinFeePredictor struct {
	fetcher      *NetworkDataFetcher
	analyzer     *FeeAnalyzer
	recommender  *FeeRecommender
	lastFeeData  *FeeEstimate
	lastNetStats *NetworkStats
	lastUpdateTime time.Time
}

// NewBitcoinFeePredictor creates and initializes a new fee predictor
func NewBitcoinFeePredictor() *BitcoinFeePredictor {
	analyzer := NewFeeAnalyzer()
	return &BitcoinFeePredictor{
		fetcher:        NewNetworkDataFetcher(),
		analyzer:       analyzer,
		recommender:    NewFeeRecommender(analyzer),
		lastUpdateTime: time.Time{},
	}
}

// FetchLatestData fetches fresh network data from mempool.space
func (bp *BitcoinFeePredictor) FetchLatestData() error {
	// Fetch recommended fees
	feeData, err := bp.fetcher.FetchRecommendedFees()
	if err != nil {
		return fmt.Errorf("failed to fetch fee data: %w", err)
	}

	// Fetch network stats
	stats, err := bp.fetcher.FetchNetworkStats()
	if err != nil {
		return fmt.Errorf("failed to fetch network stats: %w", err)
	}

	bp.lastFeeData = feeData
	bp.lastNetStats = stats
	bp.lastUpdateTime = time.Now()

	return nil
}

// PredictTransactionCost predicts the cost of sending a Bitcoin transaction
// Parameters:
//   - inputCount: Number of inputs (UTXOs being spent)
//   - outputCount: Number of outputs
//   - txType: "P2PKH", "P2SH", "P2WPKH", "P2WSH", "P2TR"
func (bp *BitcoinFeePredictor) PredictTransactionCost(inputCount int, outputCount int, txType string) (*TransactionCostEstimate, error) {
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available, call FetchLatestData first")
	}

	// Calculate virtual size
	virtualSize := bp.analyzer.CalculateVirtualSize(inputCount, outputCount, txType)

	// Analyze and predict
	estimate := bp.analyzer.AnalyzeAndPredict(bp.lastFeeData, bp.lastNetStats, virtualSize, txType)

	return estimate, nil
}

// GetFeeRecommendations returns all fee tier recommendations for a transaction
func (bp *BitcoinFeePredictor) GetFeeRecommendations(inputCount int, outputCount int, txType string) ([]*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}

	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}

	// Get all recommendations
	recs := bp.recommender.GetAllRecommendations(costEstimate, bp.lastFeeData)

	return recs, nil
}

// GetRecommendedFee returns the best fee recommendation based on current network state
func (bp *BitcoinFeePredictor) GetRecommendedFee(inputCount int, outputCount int, txType string) (*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}

	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}

	// Get best tier recommendation
	rec := bp.recommender.RecommendBestTier(costEstimate, bp.lastFeeData, bp.lastNetStats)

	return rec, nil
}

// GetCurrentNetworkState returns current Bitcoin network statistics
func (bp *BitcoinFeePredictor) GetCurrentNetworkState() (*NetworkStats, error) {
	if bp.lastNetStats == nil {
		return nil, fmt.Errorf("no network data available, call FetchLatestData first")
	}

	return bp.lastNetStats, nil
}

// GetCurrentFeeEstimate returns current fee tier estimates
func (bp *BitcoinFeePredictor) GetCurrentFeeEstimate() (*FeeEstimate, error) {
	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available, call FetchLatestData first")
	}

	return bp.lastFeeData, nil
}

// GetSimpleFeeEstimate provides a quick estimate without detailed cost breakdown
// Returns: (economicSatVB, standardSatVB, fastSatVB, expressSatVB)
func (bp *BitcoinFeePredictor) GetSimpleFeeEstimate() (int64, int64, int64, int64, error) {
	if bp.lastFeeData == nil {
		return 0, 0, 0, 0, fmt.Errorf("no fee data available")
	}

	return bp.lastFeeData.EconomicFee, bp.lastFeeData.StandardFee, bp.lastFeeData.FastFee, bp.lastFeeData.ExpressFee, nil
}

// EstimateCustomFee estimates cost for a custom fee rate
func (bp *BitcoinFeePredictor) EstimateCustomFee(inputCount int, outputCount int, txType string, customSatVB int64) (*FeeRecommendation, error) {
	costEstimate, err := bp.PredictTransactionCost(inputCount, outputCount, txType)
	if err != nil {
		return nil, err
	}

	if bp.lastFeeData == nil {
		return nil, fmt.Errorf("no fee data available")
	}

	rec := bp.recommender.GetCustomFee(costEstimate, bp.lastFeeData, customSatVB)

	return rec, nil
}

// GetNetworkConditionsSummary returns human-readable network status
func (bp *BitcoinFeePredictor) GetNetworkConditionsSummary() (string, error) {
	if bp.lastNetStats == nil || bp.lastFeeData == nil {
		return "", fmt.Errorf("no data available")
	}

	summary := fmt.Sprintf(
		"Bitcoin Network Status:\n"+
			"  Congestion: %s\n"+
			"  Active Transactions: %d\n"+
			"  Mempool Size: %.2f MB\n"+
			"  Block Height: %d\n"+
			"  Fee Rates (sat/vB):\n"+
			"    Economic: %d\n"+
			"    Standard: %d\n"+
			"    Fast: %d\n"+
			"    Express: %d",
		bp.lastNetStats.CongestionLevel,
		bp.lastNetStats.TransactionCount,
		float64(bp.lastNetStats.MempoolSize) / (1024 * 1024),
		bp.lastNetStats.LastBlockHeight,
		bp.lastFeeData.EconomicFee,
		bp.lastFeeData.StandardFee,
		bp.lastFeeData.FastFee,
		bp.lastFeeData.ExpressFee,
	)

	return summary, nil
}

// IsDataStale checks if fee data is older than the given duration
func (bp *BitcoinFeePredictor) IsDataStale(maxAge time.Duration) bool {
	if bp.lastUpdateTime.IsZero() {
		return true
	}
	return time.Since(bp.lastUpdateTime) > maxAge
}

// GetLastUpdateTime returns when data was last fetched
func (bp *BitcoinFeePredictor) GetLastUpdateTime() time.Time {
	return bp.lastUpdateTime
}

// GetDataAge returns how old the current data is
func (bp *BitcoinFeePredictor) GetDataAge() time.Duration {
	if bp.lastUpdateTime.IsZero() {
		return -1
	}
	return time.Since(bp.lastUpdateTime)
}
