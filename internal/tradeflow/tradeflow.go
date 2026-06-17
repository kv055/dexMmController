package tradeflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/pretradechecks"
	lnd "dexMmController/internal/wallethub"
)

// Order carries the parameters for a single arbitrage trade cycle. All fields
// must be supplied by the caller — hedge sizing and venue selection live in
// dexUnifiedApi/commons, not here.
type Order struct {
	// Provider is the CEX to trade on, e.g. "binance".
	Provider string
	// MarketSide is "bid" (we buy BTC on CEX) — only bid is handled this session.
	MarketSide string
	// BTCAmount is the spot buy size in BTC.
	BTCAmount float64
	// FiatCurrency is the quote currency for the spot buy, e.g. "EUR".
	FiatCurrency string
	// RequiredCapitalEUR is the total capital needed: spot notional + hedge collateral
	// + security deposit, as computed by dexUnifiedApi's CapitalRequirement.
	RequiredCapitalEUR float64
	// HedgeVenue is the futures venue, e.g. "binance_futures".
	HedgeVenue string
	// HedgeSide is "short" for a bid (we bought spot, now short the perp).
	HedgeSide string
	// HedgeNotionalBTC is the BTC notional to hedge.
	HedgeNotionalBTC float64
	// Leverage is the leverage to use on the hedge (Binance only; ignored by others).
	Leverage float64
}

// Result bundles the outcomes of all three steps in the CEX→wallet trade cycle.
type Result struct {
	SpotBuy    SpotBuyResult
	Hedge      HedgeResult
	Withdrawal WithdrawalResult
}

// SpotBuyResult is the trade-relevant subset of the spot order response.
type SpotBuyResult struct {
	OrderID     string  `json:"order_id"`
	Status      string  `json:"status"`
	FilledBTC   float64 `json:"filled_btc"`
	AverageRate float64 `json:"average_rate"`
}

// HedgeResult is the trade-relevant subset of the hedge open response.
type HedgeResult struct {
	Venue   string `json:"venue"`
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

// WithdrawalResult is the trade-relevant subset of the withdrawal response.
type WithdrawalResult struct {
	WithdrawalID  string  `json:"withdrawal_id"`
	WalletAddress string  `json:"wallet_address"`
	AmountBTC     float64 `json:"amount_btc"`
}

// Service is the subset of cexaccount.Service used by TradeFlow.
type Service interface {
	PreTradeCheck(ctx context.Context, rawProvider, fiatCurrency string, requiredCapitalEUR float64) (*pretradechecks.Report, error)
	MarketBuyBTC(ctx context.Context, rawProvider string, btcAmount float64, fiatCurrency string, requiredCapitalEUR float64) (*cexaccount.SpotOrderResult, error)
	OpenHedge(ctx context.Context, venue, side string, btcNotional, leverage float64) (*cexaccount.HedgeOpenResult, error)
	WithdrawSpotToLND(ctx context.Context, rawProvider string, amountBTC float64, lndAddress string) (*lnd.WithdrawFromExchangeResult, error)
}

// TradeFlow orchestrates the CEX→LND wallet leg of a DEX bid trade:
// pre-trade checks → spot buy → hedge → withdraw bought BTC to LND wallet.
type TradeFlow struct {
	svc Service
}

// New creates a TradeFlow backed by the given CEX service.
func New(svc Service) *TradeFlow {
	return &TradeFlow{svc: svc}
}

// Run executes the full bid-side trade cycle for the given order. It stops at the
// first failure and returns a descriptive error. On success it returns the bundled
// results of all three steps.
func (tf *TradeFlow) Run(ctx context.Context, order Order) (*Result, error) {
	if err := validate(order); err != nil {
		return nil, err
	}

	slog.Info("tradeflow: starting bid-side cycle",
		"provider", order.Provider,
		"btc_amount", order.BTCAmount,
		"fiat_currency", order.FiatCurrency,
		"hedge_venue", order.HedgeVenue,
	)

	// Step 1: pre-trade checks (read-only; result logged for observability).
	report, err := tf.svc.PreTradeCheck(ctx, order.Provider, order.FiatCurrency, order.RequiredCapitalEUR)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: pre-trade check error: %w", err)
	}
	if !report.OK {
		var failed []string
		for _, chk := range report.Checks {
			if !chk.Passed {
				failed = append(failed, chk.Message)
			}
		}
		return nil, fmt.Errorf("tradeflow: pre-trade checks failed: %s", strings.Join(failed, "; "))
	}
	lndAddr := strings.TrimSpace(report.LNDWithdrawAddress)
	if lndAddr == "" {
		return nil, fmt.Errorf("tradeflow: pre-trade checks passed but no LND withdrawal address was resolved")
	}
	slog.Info("tradeflow: pre-trade checks passed",
		"provider", order.Provider,
		"lnd_withdraw_address", lndAddr,
	)

	// Step 2: spot market buy.
	spot, err := tf.svc.MarketBuyBTC(ctx, order.Provider, order.BTCAmount, order.FiatCurrency, order.RequiredCapitalEUR)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: spot buy failed: %w", err)
	}
	filledBTC := order.BTCAmount
	var avgRate float64
	if spot.Execution != nil {
		filledBTC = spot.Execution.FilledBTC
		avgRate = spot.Execution.AverageRate
	}
	slog.Info("tradeflow: spot buy submitted",
		"order_id", spot.OrderID,
		"status", spot.Status,
		"filled_btc", filledBTC,
	)

	// Step 3: open short hedge position.
	hedge, err := tf.svc.OpenHedge(ctx, order.HedgeVenue, order.HedgeSide, order.HedgeNotionalBTC, order.Leverage)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: hedge open failed (spot already placed, order_id=%s): %w", spot.OrderID, err)
	}
	slog.Info("tradeflow: hedge opened",
		"venue", hedge.Venue,
		"order_id", hedge.OrderID,
		"status", hedge.Status,
	)

	// Step 4: withdraw the net filled BTC to the LND address resolved during pre-trade checks.
	wd, err := tf.svc.WithdrawSpotToLND(ctx, order.Provider, filledBTC, lndAddr)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: withdrawal failed (spot order_id=%s): %w", spot.OrderID, err)
	}
	slog.Info("tradeflow: withdrawal initiated",
		"withdrawal_id", wd.WithdrawalID,
		"wallet_address", wd.WalletAddress,
		"amount_btc", filledBTC,
	)

	return &Result{
		SpotBuy: SpotBuyResult{
			OrderID:     spot.OrderID,
			Status:      spot.Status,
			FilledBTC:   filledBTC,
			AverageRate: avgRate,
		},
		Hedge: HedgeResult{
			Venue:   hedge.Venue,
			OrderID: hedge.OrderID,
			Status:  hedge.Status,
		},
		Withdrawal: WithdrawalResult{
			WithdrawalID:  wd.WithdrawalID,
			WalletAddress: wd.WalletAddress,
			AmountBTC:     filledBTC,
		},
	}, nil
}

func validate(o Order) error {
	if strings.ToLower(strings.TrimSpace(o.MarketSide)) != "bid" {
		return fmt.Errorf("tradeflow: only market side \"bid\" is supported, got %q", o.MarketSide)
	}
	if strings.TrimSpace(o.Provider) == "" {
		return fmt.Errorf("tradeflow: provider is required")
	}
	if o.BTCAmount <= 0 {
		return fmt.Errorf("tradeflow: btc_amount must be greater than zero")
	}
	if o.RequiredCapitalEUR <= 0 {
		return fmt.Errorf("tradeflow: required_capital_eur must be greater than zero")
	}
	if strings.TrimSpace(o.HedgeVenue) == "" {
		return fmt.Errorf("tradeflow: hedge_venue is required")
	}
	if strings.TrimSpace(o.HedgeSide) == "" {
		return fmt.Errorf("tradeflow: hedge_side is required")
	}
	if o.HedgeNotionalBTC <= 0 {
		return fmt.Errorf("tradeflow: hedge_notional_btc must be greater than zero")
	}
	return nil
}
