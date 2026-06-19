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
	// MarketSide is "bid" (buy BTC on CEX) or "ask" (sell BTC on CEX).
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

	// OfferID is the DEX-native offer id to fund after CEX→LND withdrawal confirms.
	OfferID string
	// Dex is the DEX venue slug, e.g. "hodlhodl".
	Dex string
	// FiatAmount is the fiat notional for TakeOffer (HodlHodl).
	FiatAmount float64
	// TransactionID optionally updates an existing transaction row instead of creating one.
	TransactionID uint
	// SelectedOffer is an optional JSON snapshot of the chosen offer from the UI.
	SelectedOffer []byte
}

// Result bundles the outcomes of all steps in the CEX→wallet trade cycle.
type Result struct {
	SpotBuy       *cexaccount.SpotOrderResult `json:"spot_buy"`
	Hedge         *cexaccount.HedgeOpenResult `json:"hedge"`
	Withdrawal    WithdrawalResult            `json:"withdrawal"`
	TransactionID uint                        `json:"transaction_id,omitempty"`
}

// WithdrawalResult is the trade-relevant subset of the withdrawal response.
type WithdrawalResult struct {
	Provider        string  `json:"provider"`
	WithdrawalID    string  `json:"withdrawal_id"`
	ClientRequestID string  `json:"client_request_id,omitempty"`
	WalletAddress   string  `json:"wallet_address"`
	AmountBTC       float64 `json:"amount_btc"`
	CexStatus       string  `json:"cex_status,omitempty"`
}

// Service is the subset of cexaccount.Service used by TradeFlow.
type Service interface {
	PreTradeCheck(ctx context.Context, rawProvider, fiatCurrency string, requiredCapitalEUR float64) (*pretradechecks.Report, error)
	MarketBuyBTC(ctx context.Context, rawProvider string, btcAmount float64, fiatCurrency string, requiredCapitalEUR float64) (*cexaccount.SpotOrderResult, error)
	OpenHedge(ctx context.Context, venue, side string, btcNotional, leverage float64) (*cexaccount.HedgeOpenResult, error)
	WithdrawSpotToLND(ctx context.Context, rawProvider string, amountBTC float64, lndAddress string) (*lnd.WithdrawFromExchangeResult, error)
}

// TradeFlow orchestrates the CEX trade leg of a DEX arbitrage cycle.
type TradeFlow struct {
	svc Service
}

// New creates a TradeFlow backed by the given CEX service.
func New(svc Service) *TradeFlow {
	return &TradeFlow{svc: svc}
}

// Run executes the trade cycle for the given order. It stops at the first failure
// and returns a descriptive error. On success it returns the bundled results.
func (tf *TradeFlow) Run(ctx context.Context, order Order) (*Result, error) {
	if err := validate(order); err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(order.MarketSide)) {
	case "bid":
		return tf.runBid(ctx, order)
	case "ask":
		return tf.runAsk(ctx, order)
	default:
		return nil, fmt.Errorf("tradeflow: unsupported market side %q", order.MarketSide)
	}
}

// runBid executes the bid-side cycle: pre-trade checks → spot buy → hedge → withdraw.
func (tf *TradeFlow) runBid(ctx context.Context, order Order) (*Result, error) {
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
	if spot.Execution != nil {
		filledBTC = spot.Execution.FilledBTC
	}
	slog.Info("tradeflow: spot buy submitted",
		"order_id", spot.OrderID,
		"status", spot.Status,
		"filled_btc", filledBTC,
	)

	// Step 3: open hedge position.
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
		SpotBuy: spot,
		Hedge:   hedge,
		Withdrawal: WithdrawalResult{
			Provider:        order.Provider,
			WithdrawalID:    wd.WithdrawalID,
			ClientRequestID: wd.ClientRequestID,
			WalletAddress:   wd.WalletAddress,
			AmountBTC:       filledBTC,
			CexStatus:       "submitted",
		},
	}, nil
}

// runAsk will execute the ask-side cycle (sell BTC on CEX, long hedge, deposit to CEX).
// Planned pre-trade checks: API key permissions, sufficient CEX capital for the long
// hedge (and security deposit when the offer requires one). Whitelist/LND withdrawal
// checks are not needed on the ask path.
func (tf *TradeFlow) runAsk(_ context.Context, _ Order) (*Result, error) {
	return nil, fmt.Errorf("tradeflow: ask-side trade cycle is not implemented yet")
}

func validate(o Order) error {
	side := strings.ToLower(strings.TrimSpace(o.MarketSide))
	if side != "bid" && side != "ask" {
		return fmt.Errorf("tradeflow: market_side must be \"bid\" or \"ask\", got %q", o.MarketSide)
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
