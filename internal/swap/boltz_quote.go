package swap

import (
	"context"
	"fmt"
	"math"
	"time"

	boltz "github.com/BoltzExchange/boltz-client/v2/pkg/boltz"
)

// BoltzQuoteSource uses the official Boltz Client Go library. The controller
// never calls Boltz REST endpoints itself.
type BoltzQuoteSource struct {
	API         *boltz.Api
	Lifetime    time.Duration
	SatPerVByte int64
}

func (b BoltzQuoteSource) Quote(ctx context.Context, outputSat int64) (Quote, error) {
	if b.API == nil {
		return Quote{}, fmt.Errorf("boltz API client is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Quote{}, err
	}
	pairs, err := b.API.GetSubmarinePairs()
	if err != nil {
		return Quote{}, err
	}
	to, ok := pairs[boltz.CurrencyBtc]
	if !ok {
		return Quote{}, fmt.Errorf("boltz BTC submarine pair is unavailable")
	}
	pair, ok := to[boltz.CurrencyBtc]
	if !ok {
		return Quote{}, fmt.Errorf("boltz BTC/BTC submarine pair is unavailable")
	}
	if outputSat < int64(pair.Limits.Minimal) || outputSat > int64(pair.Limits.Maximal) {
		return Quote{}, fmt.Errorf("boltz amount is outside pair limits")
	}
	service := int64(math.Ceil(float64(outputSat) * pair.Fees.Percentage / 100))
	lockup := outputSat + service + int64(pair.Fees.MinerFees)
	rate := b.SatPerVByte
	if rate <= 0 {
		rate = 2
	}
	fundingFee := rate * 150
	life := b.Lifetime
	if life <= 0 {
		life = 2 * time.Minute
	}
	return Quote{LightningOutputSat: outputSat, LockupAmountSat: lockup, ServiceFeeSat: service, MinerFeeSat: int64(pair.Fees.MinerFees), FundingTxFeeSat: fundingFee, ExpiresAt: time.Now().Add(life)}, nil
}
