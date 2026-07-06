package tradeflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/txroute"
	lnd "dexMmController/internal/wallethub"
)

// Tunables for the guarded hedge retry. A hedge failure after the spot buy leaves
// the account holding unhedged BTC, so we retry a few times — but only after
// confirming with the venue that the failed attempt did not actually open the
// position (avoiding a double-open from a lost confirmation).
const (
	hedgeMaxAttempts = 3
	hedgeRetryDelay  = 750 * time.Millisecond
	// hedgeOpenedFraction is how much of the requested notional must appear on the
	// venue (vs. the pre-attempt baseline) to conclude the order actually filled.
	hedgeOpenedFraction = 0.9
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
	// PaymentMethod is the canonical offer payment method the operator will use to pay the maker.
	PaymentMethod string
	// PaymentMethods is the offer's full accepted-method list; when PaymentMethod
	// is empty the highest-priority linked+active catalog entry is picked from it.
	PaymentMethods []string
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
	PreTradeCheck(ctx context.Context, rawProvider, fiatCurrency string, requiredCapitalEUR float64, paymentMethod string, paymentMethods []string) (*cexaccount.Report, error)
	MarketBuyBTC(ctx context.Context, rawProvider string, btcAmount float64, fiatCurrency string, requiredCapitalEUR float64, paymentMethod string, paymentMethods []string) (*cexaccount.SpotOrderResult, error)
	OpenHedge(ctx context.Context, venue, side string, btcNotional, leverage float64) (*cexaccount.HedgeOpenResult, error)
	LiveHedgePosition(ctx context.Context, venue string) (*cexaccount.HedgeSnapshot, error)
	WithdrawSpotToLND(ctx context.Context, rawProvider string, amountBTC float64, lndAddress, clientRequestID string) (*lnd.WithdrawFromExchangeResult, error)
	WithdrawalAlreadySent(ctx context.Context, rawProvider, clientRequestID string, amountBTC float64, address string, within time.Duration) (*cexaccount.WithdrawalStatus, error)
}

// DirectDestinationResolver resolves the on-chain address the CEX should withdraw to
// for a direct-routed DEX (bisq v1: its integrated wallet funding address), where the
// CEX funds the DEX wallet straight away with no LND hub hop. Only consulted for the
// direct route.
type DirectDestinationResolver interface {
	DirectDestination(ctx context.Context, dexSlug string) (string, error)
}

// TradeFlow orchestrates the CEX trade leg of a DEX arbitrage cycle.
type TradeFlow struct {
	svc            Service
	store          transactionStore
	directResolver DirectDestinationResolver
}

// New creates a TradeFlow backed by the given CEX service and transaction store.
// store may be nil for callers that don't persist (e.g. some tests); persistence
// is also skipped when an order carries no offer_id/dex linkage.
func New(svc Service, store transactionStore) *TradeFlow {
	return &TradeFlow{svc: svc, store: store}
}

// WithDirectDestinationResolver wires the resolver used to find the withdrawal target
// for direct-routed DEXes (bisq v1). Without it, direct-route orders fail fast.
func (tf *TradeFlow) WithDirectDestinationResolver(r DirectDestinationResolver) *TradeFlow {
	tf.directResolver = r
	return tf
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
// Each irreversible step is persisted immediately after it completes, so a partial
// cycle is recoverable from the database rather than only existing in this request.
func (tf *TradeFlow) runBid(ctx context.Context, order Order) (*Result, error) {
	slog.Info("tradeflow: starting bid-side cycle",
		"provider", order.Provider,
		"btc_amount", order.BTCAmount,
		"fiat_currency", order.FiatCurrency,
		"hedge_venue", order.HedgeVenue,
	)

	// The route decides where the CEX withdraws to: direct DEXes (bisq v1) fund their
	// own wallet, hub/swap DEXes fund our LND hub.
	method := txroute.BidMethod(order.Dex)

	// Step 1: pre-trade checks (read-only; result logged for observability).
	report, err := tf.svc.PreTradeCheck(ctx, order.Provider, order.FiatCurrency, order.RequiredCapitalEUR, order.PaymentMethod, order.PaymentMethods)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: pre-trade check error: %w", err)
	}
	if ok, failed := preTradePassed(report, method); !ok {
		return nil, fmt.Errorf("tradeflow: pre-trade checks failed: %s", strings.Join(failed, "; "))
	}
	// If the caller did not explicitly pick a payment method, the pre-trade check
	// resolved the highest-priority linked+active catalog entry. Persist that name so
	// chat-venue DEXes can pick the correct template later.
	if strings.TrimSpace(order.PaymentMethod) == "" && strings.TrimSpace(report.ResolvedPaymentMethod) != "" {
		order.PaymentMethod = report.ResolvedPaymentMethod
	}

	// target is the address the CEX withdrawal is sent to.
	target, err := tf.resolveWithdrawTarget(ctx, order, method, report)
	if err != nil {
		return nil, err
	}
	slog.Info("tradeflow: pre-trade checks passed",
		"provider", order.Provider,
		"route_method", string(method),
		"withdraw_target", target,
	)

	// Persistence is enabled only when the order links a DEX offer and a store is
	// configured; otherwise the flow runs without recording (legacy behaviour).
	persist := tf.store != nil && strings.TrimSpace(order.OfferID) != "" && strings.TrimSpace(order.Dex) != ""

	var txID uint
	if persist {
		txID, err = createTradeflowRow(tf.store, order, target, method)
		if err != nil {
			return nil, fmt.Errorf("tradeflow: persist tradeflow start: %w", err)
		}
		slog.Info("tradeflow: transaction row created", "transaction_id", txID, "status", "CexTradeflowStarted")
	}

	// Step 2: spot market buy.
	spot, err := tf.svc.MarketBuyBTC(ctx, order.Provider, order.BTCAmount, order.FiatCurrency, order.RequiredCapitalEUR, order.PaymentMethod, order.PaymentMethods)
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
	if persist {
		if err := recordSpotBuy(tf.store, txID, spot, filledBTC); err != nil {
			return nil, fmt.Errorf("tradeflow: persist spot buy (order_id=%s): %w", spot.OrderID, err)
		}
	}

	// Step 3: open hedge with a guarded retry. From here on the spot is filled, so a
	// hard failure means we hold unhedged BTC — recorded as HedgeFailedNeedsReview.
	hedge, err := tf.openHedgeWithRetry(ctx, order)
	if err != nil {
		if persist {
			if perr := markHedgeFailed(tf.store, txID, spot); perr != nil {
				slog.Error("tradeflow: failed to persist HedgeFailedNeedsReview", "transaction_id", txID, "err", perr)
			}
		}
		slog.Error("tradeflow: HEDGE FAILED after spot fill — account holds unhedged BTC, manual review required",
			"transaction_id", txID,
			"spot_order_id", spot.OrderID,
			"filled_btc", filledBTC,
			"hedge_venue", order.HedgeVenue,
			"err", err,
		)
		return nil, fmt.Errorf("tradeflow: hedge open failed (spot filled, order_id=%s, needs review): %w", spot.OrderID, err)
	}
	slog.Info("tradeflow: hedge opened",
		"venue", hedge.Venue,
		"order_id", hedge.OrderID,
		"status", hedge.Status,
	)
	if persist {
		if err := recordHedge(tf.store, txID, order, spot, hedge); err != nil {
			return nil, fmt.Errorf("tradeflow: persist hedge: %w", err)
		}
	}

	// Step 4: withdraw the net filled BTC to the LND address resolved during pre-trade
	// checks. The client request id is derived from the transaction id so a retry is
	// idempotent at the exchange (no double withdrawal).
	clientReqID := ""
	if txID > 0 {
		clientReqID = fmt.Sprintf("tradeflow-%d", txID)
	}
	wd, err := tf.svc.WithdrawSpotToLND(ctx, order.Provider, filledBTC, target, clientReqID)
	if err != nil {
		return nil, fmt.Errorf("tradeflow: withdrawal failed (spot order_id=%s, hedge open): %w", spot.OrderID, err)
	}
	slog.Info("tradeflow: withdrawal initiated",
		"withdrawal_id", wd.WithdrawalID,
		"wallet_address", wd.WalletAddress,
		"amount_btc", filledBTC,
	)

	result := &Result{
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
		TransactionID: txID,
	}

	if persist {
		if err := recordWithdrawal(tf.store, txID, order, spot, hedge, result, target, method); err != nil {
			return nil, fmt.Errorf("tradeflow: persist withdrawal: %w", err)
		}
	}

	return result, nil
}

// preTradePassed reports whether the pre-trade checks allow the trade to proceed,
// returning the messages of any blocking failures. The "whitelist" check verifies that
// an LND hub address is whitelisted at the CEX — only relevant for hub/swap routes; the
// direct route withdraws to the bisq wallet instead, so its failure is not blocking.
func preTradePassed(report *cexaccount.Report, method txroute.Method) (bool, []string) {
	var failed []string
	for _, chk := range report.Checks {
		if chk.Passed {
			continue
		}
		if method == txroute.Direct && chk.Name == "whitelist" {
			continue
		}
		failed = append(failed, chk.Message)
	}
	return len(failed) == 0, failed
}

// resolveWithdrawTarget returns the address the CEX withdrawal is sent to: the bisq
// wallet's funding address for the direct route, or the whitelisted LND hub address for
// the hub/swap routes.
func (tf *TradeFlow) resolveWithdrawTarget(ctx context.Context, order Order, method txroute.Method, report *cexaccount.Report) (string, error) {
	if method == txroute.Direct {
		if tf.directResolver == nil {
			return "", fmt.Errorf("tradeflow: direct route (%s) requires a DEX wallet resolver, none configured", order.Dex)
		}
		addr, err := tf.directResolver.DirectDestination(ctx, order.Dex)
		if err != nil {
			return "", fmt.Errorf("tradeflow: resolve direct destination for %s: %w", order.Dex, err)
		}
		if strings.TrimSpace(addr) == "" {
			return "", fmt.Errorf("tradeflow: direct route resolved an empty destination address for %s", order.Dex)
		}
		return strings.TrimSpace(addr), nil
	}

	addr := strings.TrimSpace(report.LNDWithdrawAddress)
	if addr == "" {
		return "", fmt.Errorf("tradeflow: pre-trade checks passed but no LND withdrawal address was resolved")
	}
	return addr, nil
}

// openHedgeWithRetry opens the hedge, retrying on error. Before each retry it asks
// the venue whether the failed attempt actually opened the position (a lost
// confirmation can make a successful open look like a failure); if so it stops
// rather than opening a second, doubling-up hedge.
func (tf *TradeFlow) openHedgeWithRetry(ctx context.Context, order Order) (*cexaccount.HedgeOpenResult, error) {
	side := strings.ToLower(strings.TrimSpace(order.HedgeSide))

	// Baseline: how much is already open on the expected side before we try. A
	// fresh trade is normally flat, but this also tolerates pre-existing positions.
	baseline := tf.sideSizeBTC(ctx, order.HedgeVenue, side)

	var lastErr error
	for attempt := 1; attempt <= hedgeMaxAttempts; attempt++ {
		hedge, err := tf.svc.OpenHedge(ctx, order.HedgeVenue, order.HedgeSide, order.HedgeNotionalBTC, order.Leverage)
		if err == nil {
			return hedge, nil
		}
		lastErr = err
		slog.Warn("tradeflow: hedge attempt failed",
			"attempt", attempt,
			"venue", order.HedgeVenue,
			"err", err,
		)

		// Did the attempt actually open despite the error? Query the venue directly.
		snap, perr := tf.svc.LiveHedgePosition(ctx, order.HedgeVenue)
		if perr != nil {
			slog.Warn("tradeflow: live hedge position check failed; cannot confirm fill",
				"venue", order.HedgeVenue, "err", perr)
		} else if snap.Side == side && (snap.SizeBTC-baseline) >= order.HedgeNotionalBTC*hedgeOpenedFraction {
			slog.Warn("tradeflow: hedge call errored but venue shows the position is open; treating as success",
				"venue", order.HedgeVenue,
				"live_size_btc", snap.SizeBTC,
				"baseline_btc", baseline,
				"expected_btc", order.HedgeNotionalBTC,
			)
			return &cexaccount.HedgeOpenResult{
				Venue:       order.HedgeVenue,
				Side:        side,
				BTCNotional: snap.SizeBTC - baseline,
				Status:      "open_detected_after_error",
			}, nil
		}

		if attempt < hedgeMaxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(hedgeRetryDelay):
			}
		}
	}
	return nil, fmt.Errorf("hedge open failed after %d attempts: %w", hedgeMaxAttempts, lastErr)
}

// sideSizeBTC returns the live position size on the given side, or 0 if flat, the
// side differs, or the venue query fails (best-effort baseline).
func (tf *TradeFlow) sideSizeBTC(ctx context.Context, venue, side string) float64 {
	snap, err := tf.svc.LiveHedgePosition(ctx, venue)
	if err != nil || snap == nil || snap.Side != side {
		return 0
	}
	return snap.SizeBTC
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
