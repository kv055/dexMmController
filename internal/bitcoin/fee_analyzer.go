package bitcoin

import (
	"fmt"
	"time"
)

// FeeAnalyzer processes network data to derive fee predictions
type FeeAnalyzer struct {
	feeEstimate *FeeEstimate
	mempoolData *MempoolData
	networkStats *NetworkStats
}

// NewFeeAnalyzer creates a new fee analyzer
func NewFeeAnalyzer() *FeeAnalyzer {
	return &FeeAnalyzer{}
}

// AnalyzeFeeDistribution analyzes current fee distribution in mempool
func (fa *FeeAnalyzer) AnalyzeFeeDistribution(data *MempoolData) error {
	if data == nil {
		return fmt.Errorf("mempool data cannot be nil")
	}

	fa.mempoolData = data

	// Validate fee distribution
	if data.AverageFeeRate == 0 && data.MinFeeRate == 0 && data.MaxFeeRate == 0 {
		return fmt.Errorf("invalid fee distribution data")
	}

	return nil
}

// CalculateVirtualSize calculates transaction virtual size (vB) accounting for SegWit
// Formula: size = (base_size * 3 + total_size) / 4
// For estimation: P2WPKH ~68% of legacy size, P2PKH 100%, P2SH ~75%
func (fa *FeeAnalyzer) CalculateVirtualSize(inputCount int, outputCount int, txType string) int64 {
	// Simplified virtual size calculation based on transaction type
	var baseSize int64

	// Standard transaction structure
	// Version (4) + Input count varint (1) + Inputs + Output count varint (1) + Outputs + Locktime (4)
	baseSize = 10 // Version + counts + locktime

	// Add input sizes
	switch txType {
	case "P2PKH":
		// Signature: 73 bytes, PublicKey: 33 bytes, Script opcodes: ~25 bytes
		baseSize += int64(inputCount) * 150
	case "P2SH":
		// Similar to P2PKH
		baseSize += int64(inputCount) * 150
	case "P2WPKH":
		// SegWit: witness script smaller, discount applied
		baseSize += int64(inputCount) * 68 // ~45% of legacy
	case "P2WSH":
		// Native SegWit
		baseSize += int64(inputCount) * 70
	case "P2TR":
		// Taproot (smallest)
		baseSize += int64(inputCount) * 65
	default:
		// Default to P2PKH
		baseSize += int64(inputCount) * 150
	}

	// Add output sizes
	// Each output: Value (8) + Script pubkey length + Script (~25 bytes average)
	baseSize += int64(outputCount) * 34

	// Apply SegWit discount if applicable
	virtualSize := baseSize
	if txType == "P2WPKH" || txType == "P2WSH" || txType == "P2TR" {
		// SegWit discount: ~25% reduction (3:1 weight ratio)
		virtualSize = (baseSize * 3 / 4)
	}

	return virtualSize
}

// EstimateFeeForSpeed estimates fee required for specific confirmation target
// target is number of blocks expected for confirmation
func (fa *FeeAnalyzer) EstimateFeeForSpeed(feeEstimate *FeeEstimate, target int) int64 {
	if feeEstimate == nil {
		return 0
	}

	// Map confirmation blocks to fee tier
	switch {
	case target <= 1:
		return feeEstimate.ExpressFee
	case target <= 2:
		return feeEstimate.FastFee
	case target <= 6:
		return feeEstimate.StandardFee
	default:
		return feeEstimate.EconomicFee
	}
}

// CalculateTransactionFees calculates actual fees for transaction in all tiers
func (fa *FeeAnalyzer) CalculateTransactionFees(txVirtualSize int64, feeEstimate *FeeEstimate) *TransactionCostEstimate {
	if feeEstimate == nil || txVirtualSize == 0 {
		return nil
	}

	// Fee calculation: (sat/vB * vB) / 100,000,000 = BTC
	const satoshiPerBTC = 100_000_000

	estimate := &TransactionCostEstimate{
		TransactionSize: txVirtualSize,
		Timestamp:       time.Now(),
	}

	// Calculate fees in satoshis (sat/vB * vB)
	estimate.EconomicFeeSat = feeEstimate.EconomicFee * txVirtualSize
	estimate.StandardFeeSat = feeEstimate.StandardFee * txVirtualSize
	estimate.FastFeeSat = feeEstimate.FastFee * txVirtualSize
	estimate.ExpressFeeSat = feeEstimate.ExpressFee * txVirtualSize

	// Convert to BTC
	estimate.EconomicFee = float64(estimate.EconomicFeeSat) / satoshiPerBTC
	estimate.StandardFee = float64(estimate.StandardFeeSat) / satoshiPerBTC
	estimate.FastFee = float64(estimate.FastFeeSat) / satoshiPerBTC
	estimate.ExpressFee = float64(estimate.ExpressFeeSat) / satoshiPerBTC

	// Estimate confirmation times based on fee tier
	estimate.EconomicTime = 60 * time.Minute   // 1 hour+
	estimate.StandardTime = 30 * time.Minute   // 30 minutes
	estimate.FastTime = 10 * time.Minute       // 10 minutes
	estimate.ExpressTime = 2 * time.Minute     // ~next block

	// Set recommended tier (standard is balanced default)
	estimate.RecommendedTier = "standard"
	estimate.EstimatedConfidence = 0.90

	return estimate
}

// DetermineCongestionLevel analyzes network stats to determine congestion
func (fa *FeeAnalyzer) DetermineCongestionLevel(stats *NetworkStats) string {
	if stats == nil {
		return "unknown"
	}

	// Mempool size thresholds (in MB)
	const lowThreshold = 5
	const mediumThreshold = 50
	const highThreshold = 100

	mempoolMB := stats.MempoolSize / (1024 * 1024)

	switch {
	case mempoolMB < lowThreshold:
		return "low"
	case mempoolMB < mediumThreshold:
		return "low_medium"
	case mempoolMB < highThreshold:
		return "medium"
	default:
		return "high"
	}
}

// CalculateConfirmationProbability calculates probability of confirmation within N blocks
// Returns array where index represents block number (0-10 blocks)
func (fa *FeeAnalyzer) CalculateConfirmationProbability(feeRate int64, estimate *FeeEstimate) []float64 {
	probabilities := make([]float64, 11) // 0-10 blocks

	// Base probabilities depending on fee relative to tiers
	for i := 0; i <= 10; i++ {
		if feeRate >= estimate.ExpressFee {
			// Express tier: high probability in first block
			probabilities[i] = 1.0 - (float64(i) * 0.05)
		} else if feeRate >= estimate.FastFee {
			// Fast tier: high probability within 2 blocks
			if i <= 2 {
				probabilities[i] = 1.0 - (float64(i) * 0.1)
			} else {
				probabilities[i] = 0.8 - (float64(i-2) * 0.05)
			}
		} else if feeRate >= estimate.StandardFee {
			// Standard tier: reasonable probability within 6 blocks
			if i <= 6 {
				probabilities[i] = 0.9 - (float64(i) * 0.05)
			} else {
				probabilities[i] = 0.6 - (float64(i-6) * 0.03)
			}
		} else if feeRate >= estimate.EconomicFee {
			// Economic tier: slower confirmation
			if i <= 10 {
				probabilities[i] = 0.7 - (float64(i) * 0.05)
			}
		} else {
			// Below economic: risky
			probabilities[i] = 0.3 - (float64(i) * 0.02)
		}

		// Clamp to 0-1 range
		if probabilities[i] < 0 {
			probabilities[i] = 0
		}
		if probabilities[i] > 1 {
			probabilities[i] = 1
		}
	}

	return probabilities
}

// BlendEstimates combines multiple data sources for refined estimate (simple version for now)
func (fa *FeeAnalyzer) BlendEstimates(estimate *FeeEstimate, stats *NetworkStats) *FeeEstimate {
	if estimate == nil {
		return nil
	}

	// For now, just apply congestion adjustment
	if stats != nil && stats.CongestionLevel == "high" {
		// Add 20% buffer during high congestion
		estimate.EconomicFee = estimate.EconomicFee * 120 / 100
		estimate.StandardFee = estimate.StandardFee * 120 / 100
		estimate.FastFee = estimate.FastFee * 120 / 100
		estimate.ExpressFee = estimate.ExpressFee * 120 / 100
		estimate.Confidence = estimate.Confidence * 0.9 // Reduce confidence during high activity
	}

	return estimate
}

// AnalyzeAndPredict performs full analysis to produce transaction cost estimate
func (fa *FeeAnalyzer) AnalyzeAndPredict(feeEstimate *FeeEstimate, stats *NetworkStats, txVirtualSize int64, txType string) *TransactionCostEstimate {
	if feeEstimate == nil {
		return nil
	}

	// Blend estimates with network state
	blendedEstimate := fa.BlendEstimates(feeEstimate, stats)

	// Calculate costs
	costEstimate := fa.CalculateTransactionFees(txVirtualSize, blendedEstimate)

	if costEstimate != nil {
		// Add network info
		if stats != nil {
			costEstimate.NetworkCongestion = fa.DetermineCongestionLevel(stats)
		} else {
			costEstimate.NetworkCongestion = "unknown"
		}

		// Add confidence
		if feeEstimate != nil {
			costEstimate.EstimatedConfidence = feeEstimate.Confidence
		}
	}

	return costEstimate
}
