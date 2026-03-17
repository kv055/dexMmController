package bitcoin

import (
	"fmt"
	"time"
)

// FeeRecommender generates user-friendly fee recommendations
type FeeRecommender struct {
	analyzer *FeeAnalyzer
}

// NewFeeRecommender creates a new fee recommender
func NewFeeRecommender(analyzer *FeeAnalyzer) *FeeRecommender {
	return &FeeRecommender{
		analyzer: analyzer,
	}
}

// GetEconomicFee returns the most cost-effective fee recommendation
// Best for: non-urgent transactions, can wait 1+ hour
func (fr *FeeRecommender) GetEconomicFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	rec := &FeeRecommendation{
		Tier:         "economic",
		FeeRateSatVB: estimate.EconomicFee,
		TotalFeeBTC:  cost.EconomicFee,
		TotalFeeSat:  cost.EconomicFeeSat,
		EstimatedTime: cost.EconomicTime,
		Rationale:    fmt.Sprintf("Slowest option (1+ hour) but cheapest. %.8f BTC total.", cost.EconomicFee),
		RiskLevel:    "medium",
	}

	// Add probability by block
	probs := fr.analyzer.CalculateConfirmationProbability(estimate.EconomicFee, estimate)
	rec.ProbabilityByBlock = probs

	return rec
}

// GetStandardFee returns balanced recommendation (default)
// Best for: normal transactions, typical usage
func (fr *FeeRecommender) GetStandardFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	rec := &FeeRecommendation{
		Tier:         "standard",
		FeeRateSatVB: estimate.StandardFee,
		TotalFeeBTC:  cost.StandardFee,
		TotalFeeSat:  cost.StandardFeeSat,
		EstimatedTime: cost.StandardTime,
		Rationale:    fmt.Sprintf("Balanced option (10-60 min). Recommended for most transactions. %.8f BTC total.", cost.StandardFee),
		RiskLevel:    "low",
	}

	// Add probability by block
	probs := fr.analyzer.CalculateConfirmationProbability(estimate.StandardFee, estimate)
	rec.ProbabilityByBlock = probs

	return rec
}

// GetFastFee returns faster confirmation recommendation
// Best for: time-sensitive transactions, willing to pay more
func (fr *FeeRecommender) GetFastFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	rec := &FeeRecommendation{
		Tier:         "fast",
		FeeRateSatVB: estimate.FastFee,
		TotalFeeBTC:  cost.FastFee,
		TotalFeeSat:  cost.FastFeeSat,
		EstimatedTime: cost.FastTime,
		Rationale:    fmt.Sprintf("Fast confirmation (<10 min). Premium of %.8f BTC vs. standard.", cost.FastFee - cost.StandardFee),
		RiskLevel:    "low",
	}

	// Add probability by block
	probs := fr.analyzer.CalculateConfirmationProbability(estimate.FastFee, estimate)
	rec.ProbabilityByBlock = probs

	return rec
}

// GetExpressFee returns fastest confirmation (next block expected)
// Best for: urgent transactions, high value trades
func (fr *FeeRecommender) GetExpressFee(cost *TransactionCostEstimate, estimate *FeeEstimate) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	premium := cost.ExpressFee - cost.StandardFee
	rec := &FeeRecommendation{
		Tier:         "express",
		FeeRateSatVB: estimate.ExpressFee,
		TotalFeeBTC:  cost.ExpressFee,
		TotalFeeSat:  cost.ExpressFeeSat,
		EstimatedTime: cost.ExpressTime,
		Rationale:    fmt.Sprintf("Fastest option (next block, ~2 min). Premium of %.8f BTC vs. standard.", premium),
		RiskLevel:    "very_low",
	}

	// Add probability by block
	probs := fr.analyzer.CalculateConfirmationProbability(estimate.ExpressFee, estimate)
	rec.ProbabilityByBlock = probs

	return rec
}

// GetCustomFee returns recommendation for user-specified fee rate
func (fr *FeeRecommender) GetCustomFee(cost *TransactionCostEstimate, estimate *FeeEstimate, customSatVB int64) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	// Calculate custom fee
	customFeeSat := customSatVB * cost.TransactionSize
	customFeeBTC := float64(customFeeSat) / 100_000_000

	// Classify custom fee tier
	tier := "custom"
	riskLevel := "medium"
	estimatedTime := 30 * time.Minute

	switch {
	case customSatVB >= estimate.ExpressFee:
		tier = "express"
		riskLevel = "very_low"
		estimatedTime = 2 * time.Minute
	case customSatVB >= estimate.FastFee:
		tier = "fast"
		riskLevel = "low"
		estimatedTime = 10 * time.Minute
	case customSatVB >= estimate.StandardFee:
		tier = "standard"
		riskLevel = "low"
		estimatedTime = 30 * time.Minute
	case customSatVB >= estimate.EconomicFee:
		tier = "economic"
		riskLevel = "medium"
		estimatedTime = 60 * time.Minute
	default:
		riskLevel = "high"
		estimatedTime = 120 * time.Minute
	}

	rationale := fmt.Sprintf("Custom fee at %d sat/vB", customSatVB)
	if customSatVB < estimate.EconomicFee {
		rationale += " (below economic tier, may not confirm)"
	} else if customSatVB > estimate.ExpressFee {
		rationale += fmt.Sprintf(" (%.8f BTC overpayment)", customFeeBTC - cost.ExpressFee)
	}

	rec := &FeeRecommendation{
		Tier:         tier,
		FeeRateSatVB: customSatVB,
		TotalFeeBTC:  customFeeBTC,
		TotalFeeSat:  customFeeSat,
		EstimatedTime: estimatedTime,
		Rationale:    rationale,
		RiskLevel:    riskLevel,
	}

	// Add probability by block
	probs := fr.analyzer.CalculateConfirmationProbability(customSatVB, estimate)
	rec.ProbabilityByBlock = probs

	return rec
}

// GetAllRecommendations returns all four standard tier recommendations
func (fr *FeeRecommender) GetAllRecommendations(cost *TransactionCostEstimate, estimate *FeeEstimate) []*FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	return []*FeeRecommendation{
		fr.GetEconomicFee(cost, estimate),
		fr.GetStandardFee(cost, estimate),
		fr.GetFastFee(cost, estimate),
		fr.GetExpressFee(cost, estimate),
	}
}

// RecommendBestTier recommends optimal tier based on network conditions
func (fr *FeeRecommender) RecommendBestTier(cost *TransactionCostEstimate, estimate *FeeEstimate, stats *NetworkStats) *FeeRecommendation {
	if cost == nil || estimate == nil {
		return nil
	}

	// Choose tier based on network congestion
	if stats == nil {
		// Default to standard if no stats
		return fr.GetStandardFee(cost, estimate)
	}

	switch stats.CongestionLevel {
	case "low":
		// Low congestion: economic is safe
		return fr.GetEconomicFee(cost, estimate)
	case "low_medium":
		// Light congestion: still okay for economic
		return fr.GetEconomicFee(cost, estimate)
	case "medium":
		// Medium congestion: standard recommended
		return fr.GetStandardFee(cost, estimate)
	case "high":
		// High congestion: fast recommended to ensure confirmation
		return fr.GetFastFee(cost, estimate)
	default:
		// Unknown: default to standard
		return fr.GetStandardFee(cost, estimate)
	}
}
