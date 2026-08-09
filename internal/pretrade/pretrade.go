// Package pretrade contains the pre-trade validation contract, dependency
// interfaces, and checker implementation used by the MM controller.
package pretrade

import (
	"context"
	"fmt"
	"strings"
)

const (
	TradeModeHedgefullSingleCollateral = "hedgefull_single_collateral"
	TradeModeHedgelessDoubleCollateral = "hedgeless_double_collateral"
)

// Params is the complete input to a pre-trade policy evaluation. MarketSide is
// deliberately neutral so the same checker contract can serve bid and ask
// execution as ask-side trading is added.
type Params struct {
	Provider             string
	FiatCurrency         string
	RequiredCapital      float64
	PaymentMethod        string
	PaymentMethods       []string
	Dex                  string
	MarketSide           string
	TradeMode            string
	HedgeVenue           string
	RequiredBTCLiquidity float64
}

// CheckResult is the outcome of one named check.
type CheckResult struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Report is the complete result of a policy evaluation.
type Report struct {
	OK                    bool          `json:"ok"`
	Checks                []CheckResult `json:"checks"`
	LNDWithdrawAddress    string        `json:"lnd_withdraw_address,omitempty"`
	ResolvedPaymentMethod string        `json:"resolved_payment_method,omitempty"`
}

// Checker is the dependency used by execution flows. Run returns all checks
// for UI/observability; RunAll is the fail-fast-at-the-boundary convenience
// method used immediately before placing an order.
type Checker interface {
	Run(context.Context, Params) Report
	RunAll(context.Context, Params) error
}

func NormalizeTradeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TradeModeHedgelessDoubleCollateral, "hedgeless":
		return TradeModeHedgelessDoubleCollateral
	default:
		return TradeModeHedgefullSingleCollateral
	}
}

func ParseTradeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TradeModeHedgefullSingleCollateral, "hedgefull":
		return TradeModeHedgefullSingleCollateral, nil
	case TradeModeHedgelessDoubleCollateral, "hedgeless":
		return TradeModeHedgelessDoubleCollateral, nil
	default:
		return "", fmt.Errorf("unsupported trade_mode %q", raw)
	}
}
