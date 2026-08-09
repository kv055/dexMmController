package cexaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/cex"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/coinbase"
	"dexMmController/internal/exchangeconnectors/cex/coinbaseexchange"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
	"dexMmController/internal/pretrade"
	lnd "dexMmController/internal/wallethub"
)

// Repository loads account credentials for CEX operations.
type Repository interface {
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	GetTransactionsByCEX(cexName string) ([]db.Transaction, error)
}

// Service fetches live CEX account data for the trading UI.
type Service struct {
	repo            Repository
	cfg             Config
	preTradeChecker pretrade.Checker
	lnd             *lnd.Client
}

// WithPreTradeChecker attaches the policy checker used by PreTradeCheck.
func (s *Service) WithPreTradeChecker(c pretrade.Checker) *Service {
	s.preTradeChecker = c
	return s
}

// WithBidChecker is kept as a compatibility shim for the current wiring. The
// checker contract itself now lives in internal/pretrade.
func (s *Service) WithBidChecker(c *BidChecker) *Service {
	return s.WithPreTradeChecker(c)
}

// WithLND attaches the LND wallet client used by the withdrawal methods.
func (s *Service) WithLND(c *lnd.Client) *Service {
	s.lnd = c
	return s
}

// NewService creates a CEX account service.
func NewService(repo Repository, cfg Config) *Service {
	return &Service{repo: repo, cfg: cfg}
}

// DepositAddress is a deposit destination on a CEX.
type DepositAddress struct {
	Asset   string `json:"asset"`
	Network string `json:"network"`
	Address string `json:"address"`
	Tag     string `json:"tag,omitempty"`
	Method  string `json:"method,omitempty"`
}

// WithdrawalAddress is a saved (whitelisted) withdrawal destination on a CEX.
type WithdrawalAddress struct {
	Asset       string `json:"asset"`
	Network     string `json:"network"`
	Address     string `json:"address"`
	Tag         string `json:"tag,omitempty"`
	Label       string `json:"label,omitempty"`
	Whitelisted bool   `json:"whitelisted,omitempty"`
}

// AssetBalance is a single asset balance entry.
type AssetBalance struct {
	Asset     string  `json:"asset"`
	Available float64 `json:"available"`
	Total     float64 `json:"total"`
	USDValue  float64 `json:"usd_value,omitempty"`
}

// BalancesResponse groups BTC and fiat balances.
type BalancesResponse struct {
	Bitcoin *AssetBalance  `json:"bitcoin,omitempty"`
	Fiat    []AssetBalance `json:"fiat"`
	Source  string         `json:"source"`
}

// HedgePosition is a hedge position linked to an MM transaction.
type HedgePosition struct {
	TransactionID uint            `json:"transaction_id"`
	Status        string          `json:"status"`
	Direction     string          `json:"direction"`
	Venue         string          `json:"venue,omitempty"`
	Instrument    string          `json:"instrument,omitempty"`
	SizeBTC       float64         `json:"size_btc,omitempty"`
	Fee           float64         `json:"fee,omitempty"`
	OpenedAt      string          `json:"opened_at,omitempty"`
	ClosedAt      string          `json:"closed_at,omitempty"`
	OpenDetails   json.RawMessage `json:"open_details,omitempty"`
	CloseDetails  json.RawMessage `json:"close_details,omitempty"`
}

// HedgePositionsResponse lists active and closed hedge positions.
type HedgePositionsResponse struct {
	Active []HedgePosition `json:"active"`
	Past   []HedgePosition `json:"past"`
}

// SpotOrderResult is the normalized result of a market-buy order placed on a CEX.
type SpotOrderResult struct {
	Provider     string           `json:"provider"`
	Pair         string           `json:"pair"`
	Side         string           `json:"side"`
	RequestedBTC float64          `json:"requested_btc"`
	OrderID      string           `json:"order_id"`
	Status       string           `json:"status"`
	SubmittedAt  string           `json:"submitted_at"`
	Execution    *ExecutionDetail `json:"execution,omitempty"`
	Raw          json.RawMessage  `json:"raw,omitempty"`
}

// FeeLine is one normalized fee line item, aggregated by asset.
type FeeLine struct {
	Asset  string  `json:"asset"`  // e.g. "EUR", "USD", "BNB"
	Amount float64 `json:"amount"` // always in Asset units
}

// FeeInCurrency totals the spot fill's fee lines denominated in the given currency
// (the trade's quote/fiat currency). Fees paid in a divergent asset (e.g. Binance's
// BNB fee-discount program) are not converted and are excluded — the full breakdown
// stays in Execution.Fees / the cex_transaction_details JSON.
func (r *SpotOrderResult) FeeInCurrency(currency string) float64 {
	if r == nil || r.Execution == nil {
		return 0
	}
	currency = strings.TrimSpace(currency)
	var total float64
	for _, fee := range r.Execution.Fees {
		if strings.EqualFold(strings.TrimSpace(fee.Asset), currency) {
			total += fee.Amount
		}
	}
	return total
}

// ExecutionDetail is the normalized fill/cost/fee summary for a market-buy order,
// translated from each exchange's native response. AverageRate and FilledFiat are
// always expressed in the trade's quote currency (fiatCurrency). Fees are reported
// separately rather than netted into FilledFiat, since the fee asset can diverge
// from the quote currency (e.g. Binance's BNB fee-discount program).
type ExecutionDetail struct {
	// FilledBTC is the executed base-asset (BTC) quantity.
	FilledBTC float64 `json:"filled_btc"`
	// AverageRate is the volume-weighted average fill price, quote currency per BTC.
	AverageRate float64 `json:"average_rate"`
	// FilledFiat is the total quote-currency cost of the fill (pre-fee).
	FilledFiat float64 `json:"filled_fiat"`
	// Fees lists fee line items, summed by asset. Empty if the exchange reported no fee.
	Fees []FeeLine `json:"fees,omitempty"`
	// OrderStatus is the exchange's order status, normalized to one of: "filled",
	// "partially_filled", "open", "canceled", "expired", "unknown".
	OrderStatus string `json:"order_status"`
	// PollIncomplete is true when the bounded wait for a terminal order status was
	// exhausted (or the status lookup itself failed) before fill data could be
	// confirmed. The fields above are then best-effort/last-seen, not authoritative.
	PollIncomplete bool `json:"poll_incomplete,omitempty"`
}

// SpotTrade is a spot market fill from the exchange connector.
type SpotTrade struct {
	TradeID     string  `json:"trade_id"`
	TradingPair string  `json:"trading_pair"`
	TradeType   string  `json:"trade_type"`
	Price       float64 `json:"price"`
	Amount      float64 `json:"amount"`
	Fee         float64 `json:"fee"`
	FeeAsset    string  `json:"fee_asset,omitempty"`
	Timestamp   string  `json:"timestamp"`
}

// DepositAddresses returns BTC deposit addresses for the provider.
func (s *Service) DepositAddresses(ctx context.Context, rawProvider string) ([]DepositAddress, error) {
	provider, key, err := s.spotAPIKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(provider) {
	case "kraken":
		client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
		methods, err := client.GetDepositMethods(ctx, "XBT")
		if err != nil {
			return nil, err
		}
		if len(methods) == 0 {
			return nil, fmt.Errorf("kraken: no deposit methods available for XBT")
		}
		addresses := make([]DepositAddress, 0, len(methods))
		seen := map[string]struct{}{}
		for _, method := range methods {
			rows, err := client.GetDepositAddresses(ctx, "XBT", method.Method, false)
			if err != nil || len(rows) == 0 {
				continue
			}
			for _, row := range rows {
				if row.Address == "" {
					continue
				}
				if _, ok := seen[row.Address]; ok {
					continue
				}
				seen[row.Address] = struct{}{}
				addresses = append(addresses, DepositAddress{
					Asset:   "BTC",
					Network: method.Method,
					Address: row.Address,
					Method:  method.Method,
				})
			}
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("kraken: no deposit addresses returned for XBT")
		}
		return addresses, nil
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		resp, err := client.GetDepositAddress(ctx, "BTC", "BTC")
		if err != nil {
			return nil, err
		}
		return []DepositAddress{{
			Asset:   "BTC",
			Network: "BTC",
			Address: resp.Address,
			Tag:     resp.Tag,
		}}, nil
	case "coinbase":
		client := coinbase.NewClient(s.cfg.CoinbaseBaseURL, key.APIKey, key.APISecret)
		rows, err := client.BTCDepositAddresses(ctx)
		if err != nil {
			return nil, err
		}
		addresses := make([]DepositAddress, 0, len(rows))
		for _, row := range rows {
			network := strings.TrimSpace(row.Network)
			if network == "" {
				network = "bitcoin"
			}
			addresses = append(addresses, DepositAddress{
				Asset:   "BTC",
				Network: network,
				Address: row.Address,
				Method:  network,
			})
		}
		return addresses, nil
	default:
		return nil, fmt.Errorf("%s deposit addresses are not supported yet", provider)
	}
}

// WithdrawalAddresses returns whitelisted BTC withdrawal addresses for the provider.
func (s *Service) WithdrawalAddresses(ctx context.Context, rawProvider string) ([]WithdrawalAddress, error) {
	provider, key, err := s.spotAPIKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(provider) {
	case "kraken":
		client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
		rows, err := client.GetWithdrawAddresses(ctx, "XBT")
		if err != nil {
			return nil, err
		}
		addresses := make([]WithdrawalAddress, 0, len(rows))
		for _, row := range rows {
			if row.Address == "" {
				continue
			}
			addresses = append(addresses, WithdrawalAddress{
				Asset:       "BTC",
				Network:     row.Method,
				Address:     row.Address,
				Tag:         row.Tag,
				Label:       row.Key,
				Whitelisted: row.Verified,
			})
		}
		return addresses, nil
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		rows, err := client.WithdrawAddressList(ctx)
		if err != nil {
			return nil, err
		}
		addresses := make([]WithdrawalAddress, 0, len(rows))
		for _, row := range rows {
			if row.Address == "" || !isBitcoinAsset(row.Coin) {
				continue
			}
			addresses = append(addresses, WithdrawalAddress{
				Asset:       "BTC",
				Network:     row.Network,
				Address:     row.Address,
				Tag:         row.AddressTag,
				Label:       row.Name,
				Whitelisted: row.WhiteStatus,
			})
		}
		return addresses, nil
	case "coinbase":
		_, key, err := s.firstActiveKeyWithPassphrase(ctx, rawProvider)
		if err != nil {
			return nil, err
		}
		client := coinbaseexchange.NewClient(s.cfg.CoinbaseExchangeBaseURL, key.APIKey, key.APISecret, key.Passphrase)
		rows, err := client.AddressBook(ctx)
		if err != nil {
			return nil, err
		}
		addresses := make([]WithdrawalAddress, 0, len(rows))
		for _, row := range rows {
			if row.Address == "" || !isBitcoinAsset(row.Currency) {
				continue
			}
			addresses = append(addresses, WithdrawalAddress{
				Asset:       "BTC",
				Network:     strings.TrimSpace(row.Network),
				Address:     row.Address,
				Tag:         row.DestinationTag,
				Label:       row.Label,
				Whitelisted: true,
			})
		}
		return addresses, nil
	default:
		return nil, fmt.Errorf("%s withdrawal addresses are not supported yet", provider)
	}
}

// Balances returns BTC and fiat balances for the provider.
func (s *Service) Balances(ctx context.Context, rawProvider string) (*BalancesResponse, error) {
	provider, key, err := s.spotAPIKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(provider) {
	case "kraken":
		client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
		balances, err := client.Balance(ctx)
		if err != nil {
			return nil, err
		}
		return balancesFromKraken(balances), nil
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		balances, err := client.SpotBalances(ctx)
		if err != nil {
			return nil, err
		}
		return balancesFromBinance(balances), nil
	case "coinbase":
		client := coinbase.NewClient(s.cfg.CoinbaseBaseURL, key.APIKey, key.APISecret)
		accounts, err := client.ListAccounts(ctx)
		if err != nil {
			return nil, err
		}
		return balancesFromCoinbase(accounts), nil
	default:
		return nil, fmt.Errorf("balances not supported for provider %q", provider)
	}
}

// HedgePositions returns active and past hedge positions from MM transactions.
func (s *Service) HedgePositions(ctx context.Context, rawProvider string) (*HedgePositionsResponse, error) {
	provider, err := CanonicalProvider(rawProvider)
	if err != nil {
		return nil, err
	}
	transactions, err := s.repo.GetTransactionsByCEX(provider)
	if err != nil {
		return nil, err
	}

	resp := &HedgePositionsResponse{
		Active: []HedgePosition{},
		Past:   []HedgePosition{},
	}
	for _, tx := range transactions {
		if !hasJSONData(tx.CexCounterPositionOpenDetails) {
			continue
		}
		position := hedgePositionFromTransaction(tx)
		if hasJSONData(tx.CexCounterPositionCloseDetails) || tx.TransactionComplete {
			resp.Past = append(resp.Past, position)
			continue
		}
		resp.Active = append(resp.Active, position)
	}
	return resp, nil
}

// SpotTrades returns recent spot market fills for the provider.
func (s *Service) SpotTrades(_ context.Context, _ string, _ int) ([]SpotTrade, error) {
	return nil, errors.New("spot trades listing is not supported (hummingbot removed)")
}

// defaultMaxMarketBuyBTC is the fallback per-order cap when MAX_MARKET_BUY_BTC is unset.
const defaultMaxMarketBuyBTC = 1.0

// maxMarketBuyBTC returns the configured per-order BTC cap for market buys.
func maxMarketBuyBTC() float64 {
	raw := strings.TrimSpace(os.Getenv("MAX_MARKET_BUY_BTC"))
	if raw == "" {
		return defaultMaxMarketBuyBTC
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return defaultMaxMarketBuyBTC
	}
	return value
}

// normalizeFiat upper-cases fiatCurrency and defaults it to "EUR" when blank.
func normalizeFiat(fiatCurrency string) string {
	fiat := strings.ToUpper(strings.TrimSpace(fiatCurrency))
	if fiat == "" {
		fiat = "EUR"
	}
	return fiat
}

// marketBuyPair builds the exchange-native trading pair for buying BTC with fiatCurrency.
func marketBuyPair(provider, fiatCurrency string) string {
	fiat := normalizeFiat(fiatCurrency)
	switch strings.ToLower(provider) {
	case "kraken":
		return "XBT" + fiat
	case "coinbase":
		return "BTC-" + fiat
	default: // binance and others
		return "BTC" + fiat
	}
}

// PreTradeCheck runs all bid-side pre-trade checks to completion (without placing any
// order) and returns the per-check report so the UI can show exactly which checks pass
// or fail before the operator confirms the trade. It performs only read-only lookups.
func (s *Service) PreTradeCheck(ctx context.Context, p PreTradeParams) (*Report, error) {
	if s.preTradeChecker == nil {
		return nil, fmt.Errorf("bid pre-trade checker not configured")
	}
	if p.RequiredCapital <= 0 {
		return nil, fmt.Errorf("required_capital must be greater than zero")
	}
	report := s.preTradeChecker.Run(ctx, p)
	return &report, nil
}

// MarketBuyBTC places a market buy order for btcAmount of BTC on the given CEX provider,
// pairing BTC against fiatCurrency. It is the trade-tracing "Start trade" action.
//
// Pre-trade validation is performed by TradeFlow before calling this execution
// primitive. Keeping orchestration there makes this service responsible only
// for placing the CEX order.
func (s *Service) MarketBuyBTC(ctx context.Context, btcAmount float64, p PreTradeParams) (*SpotOrderResult, error) {
	return s.marketBuyBTC(ctx, btcAmount, p, false)
}

// MarketBuyBTCForRefill buys replacement BTC after the hedgeless LND spend.
// The complete pre-trade gate was run immediately before that irreversible DEX
// action; rerunning it here would fail its liquidity-pool check by design.
func (s *Service) MarketBuyBTCForRefill(ctx context.Context, btcAmount float64, p PreTradeParams) (*SpotOrderResult, error) {
	return s.marketBuyBTC(ctx, btcAmount, p, false)
}

func (s *Service) marketBuyBTC(ctx context.Context, btcAmount float64, p PreTradeParams, runPreTrade bool) (*SpotOrderResult, error) {
	if btcAmount <= 0 {
		return nil, fmt.Errorf("btc_amount must be greater than zero")
	}
	if cap := maxMarketBuyBTC(); btcAmount > cap {
		return nil, fmt.Errorf("btc_amount %.8f exceeds the configured per-order cap of %.8f BTC", btcAmount, cap)
	}
	if p.RequiredCapital <= 0 {
		return nil, fmt.Errorf("required_capital must be greater than zero")
	}
	if runPreTrade {
		if s.preTradeChecker == nil {
			return nil, fmt.Errorf("pre-trade checker not configured")
		}
		if err := s.preTradeChecker.RunAll(ctx, p); err != nil {
			return nil, err
		}
	}

	provider, key, err := s.spotAPIKey(ctx, p.Provider)
	if err != nil {
		return nil, err
	}
	pair := marketBuyPair(provider, p.FiatCurrency)
	result := &SpotOrderResult{
		Provider:     provider,
		Pair:         pair,
		Side:         "BUY",
		RequestedBTC: btcAmount,
		SubmittedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	quoteAsset := normalizeFiat(p.FiatCurrency)

	switch strings.ToLower(provider) {
	case "kraken":
		client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
		order, err := client.MarketBuy(ctx, pair, btcAmount)
		if err != nil {
			return nil, err
		}
		if len(order.TxID) > 0 {
			result.OrderID = order.TxID[0]
		}
		result.Status = "submitted"
		result.Raw, _ = json.Marshal(order)
		if result.OrderID != "" {
			exec := s.krakenExecution(ctx, client, result.OrderID, quoteAsset)
			result.Execution = &exec
		}
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		order, err := client.MarketBuy(ctx, pair, btcAmount)
		if err != nil {
			return nil, err
		}
		result.OrderID = strconv.FormatInt(order.OrderID, 10)
		result.Status = strings.ToLower(order.Status)
		result.Raw, _ = json.Marshal(order)
		exec, execErr := executionFromBinance(*order)
		if execErr != nil {
			exec = ExecutionDetail{OrderStatus: "unknown", PollIncomplete: true}
		}
		result.Execution = &exec
	case "coinbase":
		client := coinbase.NewClient(s.cfg.CoinbaseBaseURL, key.APIKey, key.APISecret)
		order, err := client.MarketBuy(ctx, pair, btcAmount)
		if err != nil {
			return nil, err
		}
		result.OrderID = order.SuccessResponse.OrderID
		result.Status = "submitted"
		result.Raw, _ = json.Marshal(order)
		if result.OrderID != "" {
			exec := s.coinbaseExecution(ctx, client, result.OrderID, quoteAsset)
			result.Execution = &exec
		}
	default:
		return nil, fmt.Errorf("market buy is not supported for %s yet", provider)
	}

	return result, nil
}

// ConfirmedLNDOnChainBTC returns the confirmed balance used as the durable
// hedgeless pool target.
func (s *Service) ConfirmedLNDOnChainBTC(ctx context.Context) (float64, error) {
	if s.lnd == nil {
		return 0, fmt.Errorf("LND wallet not available")
	}
	bal, err := s.lnd.OnChainBalance(ctx)
	if err != nil {
		return 0, err
	}
	if bal == nil || strings.TrimSpace(bal.ConfirmedBalance) == "" {
		return 0, nil
	}
	sats, err := strconv.ParseInt(strings.TrimSpace(bal.ConfirmedBalance), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse LND confirmed balance: %w", err)
	}
	return float64(sats) / 1e8, nil
}

// LNDPoolBalanceBTC returns the balance whose pre-spend target must be restored.
// On-chain DEXes use confirmed wallet UTXOs. RoboSats consumes local channel
// balance, so its pool target is total spendable LND BTC across both forms.
func (s *Service) LNDPoolBalanceBTC(ctx context.Context, dex string) (float64, error) {
	onChain, err := s.ConfirmedLNDOnChainBTC(ctx)
	if err != nil || !strings.EqualFold(strings.TrimSpace(dex), "robosats") {
		return onChain, err
	}
	bal, err := s.lnd.ChannelBalance(ctx)
	if err != nil {
		return 0, err
	}
	if bal == nil || strings.TrimSpace(bal.LocalBalance.Sat) == "" {
		return onChain, nil
	}
	sats, err := strconv.ParseInt(strings.TrimSpace(bal.LocalBalance.Sat), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse LND local channel balance: %w", err)
	}
	return onChain + float64(sats)/1e8, nil
}

// marketBuyPollMaxAttempts and marketBuyPollDelay bound how long MarketBuyBTC waits
// for Kraken/Coinbase to report a terminal order status before giving up and
// returning best-effort (PollIncomplete) execution detail. The order itself has
// already been placed by this point, so exhausting the budget never fails the
// overall request.
const (
	marketBuyPollMaxAttempts = 5
	marketBuyPollDelay       = 400 * time.Millisecond
)

// pollUntilTerminal calls fetch repeatedly (up to maxAttempts times, sleeping delay
// between attempts and honoring ctx cancellation) until fetch reports a terminal
// result or the attempt budget is exhausted. exhausted is true when every attempt
// returned terminal=false without error; err carries the error from the final
// attempt, if any.
func pollUntilTerminal(ctx context.Context, maxAttempts int, delay time.Duration, fetch func(ctx context.Context) (terminal bool, err error)) (exhausted bool, err error) {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		terminal, fetchErr := fetch(ctx)
		if fetchErr != nil {
			err = fetchErr
		} else {
			err = nil
			if terminal {
				return false, nil
			}
		}
		if attempt == maxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(delay):
		}
	}
	return true, err
}

// krakenExecution polls Kraken's QueryOrders for txID until the order reaches a
// terminal status (or the poll budget is exhausted), then translates whatever was
// last seen into the common ExecutionDetail schema.
func (s *Service) krakenExecution(ctx context.Context, client *kraken.Client, txID, quoteAsset string) ExecutionDetail {
	var last kraken.OrderInfo
	exhausted, err := pollUntilTerminal(ctx, marketBuyPollMaxAttempts, marketBuyPollDelay, func(ctx context.Context) (bool, error) {
		orders, err := client.QueryOrders(ctx, txID)
		if err != nil {
			return false, err
		}
		info, ok := orders[txID]
		if !ok {
			return false, fmt.Errorf("kraken: order %s not found in QueryOrders response", txID)
		}
		last = info
		switch strings.ToLower(strings.TrimSpace(info.Status)) {
		case "closed", "canceled", "expired":
			return true, nil
		default:
			return false, nil
		}
	})
	exec, translateErr := executionFromKraken(last, quoteAsset)
	if translateErr != nil {
		exec = ExecutionDetail{OrderStatus: "unknown"}
	}
	if exhausted || err != nil {
		exec.PollIncomplete = true
	}
	return exec
}

// coinbaseExecution polls Coinbase's GetOrder for orderID until the order reaches a
// terminal status (or the poll budget is exhausted), then translates whatever was
// last seen into the common ExecutionDetail schema.
func (s *Service) coinbaseExecution(ctx context.Context, client *coinbase.Client, orderID, quoteAsset string) ExecutionDetail {
	var last coinbase.Order
	exhausted, err := pollUntilTerminal(ctx, marketBuyPollMaxAttempts, marketBuyPollDelay, func(ctx context.Context) (bool, error) {
		order, err := client.GetOrder(ctx, orderID)
		if err != nil {
			return false, err
		}
		last = *order
		switch strings.ToUpper(strings.TrimSpace(order.Status)) {
		case "FILLED", "CANCELLED", "EXPIRED", "FAILED":
			return true, nil
		default:
			return false, nil
		}
	})
	exec, translateErr := executionFromCoinbase(last, quoteAsset)
	if translateErr != nil {
		exec = ExecutionDetail{OrderStatus: "unknown"}
	}
	if exhausted || err != nil {
		exec.PollIncomplete = true
	}
	return exec
}

// normalizeOrderStatus maps an exchange-specific order status string to one of the
// common ExecutionDetail.OrderStatus values: "filled", "partially_filled", "open",
// "canceled", "expired", or "unknown" for anything not recognized.
func normalizeOrderStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "CLOSED", "FILLED":
		return "filled"
	case "PARTIALLY_FILLED":
		return "partially_filled"
	case "OPEN", "PENDING", "NEW":
		return "open"
	case "CANCELED", "CANCELLED":
		return "canceled"
	case "EXPIRED":
		return "expired"
	default:
		return "unknown"
	}
}

// executionFromKraken translates a Kraken QueryOrders result for a single order into
// the common ExecutionDetail schema. quoteAsset labels the fee line, since Kraken's
// fee field is always in the pair's quote currency but doesn't carry an asset code.
func executionFromKraken(order kraken.OrderInfo, quoteAsset string) (ExecutionDetail, error) {
	volExec, err := parseKrakenFloat(order.VolExec)
	if err != nil {
		return ExecutionDetail{}, err
	}
	cost, err := parseKrakenFloat(order.Cost)
	if err != nil {
		return ExecutionDetail{}, err
	}
	price, err := parseKrakenFloat(order.Price)
	if err != nil {
		return ExecutionDetail{}, err
	}
	fee, err := parseKrakenFloat(order.Fee)
	if err != nil {
		return ExecutionDetail{}, err
	}

	status := normalizeOrderStatus(order.Status)
	if status == "filled" && volExec == 0 {
		// "closed" with nothing executed (e.g. rejected-then-closed) isn't a real fill.
		status = "unknown"
	}

	exec := ExecutionDetail{
		FilledBTC:   volExec,
		AverageRate: price,
		FilledFiat:  cost,
		OrderStatus: status,
	}
	if fee > 0 {
		exec.Fees = []FeeLine{{Asset: quoteAsset, Amount: fee}}
	}
	return exec, nil
}

// parseKrakenFloat parses one of Kraken's decimal-string numeric fields, treating an
// empty string as zero (e.g. for an order that hasn't accrued cost/fee yet).
func parseKrakenFloat(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}

// executionFromBinance translates a Binance OrderResponse (with its Fills slice) into
// the common ExecutionDetail schema. Rate/filled amounts and fees are derived from the
// same Fills array so they stay internally consistent; if Fills is empty (only possible
// if Binance ever changes its default response type for market orders), it falls back
// to the top-level ExecutedQty/CummulativeQuoteQty fields with no fee data.
func executionFromBinance(order binance.OrderResponse) (ExecutionDetail, error) {
	status := normalizeOrderStatus(order.Status)

	if len(order.Fills) == 0 {
		filledBTC, err := strconv.ParseFloat(strings.TrimSpace(order.ExecutedQty), 64)
		if err != nil {
			return ExecutionDetail{}, err
		}
		filledFiat, err := strconv.ParseFloat(strings.TrimSpace(order.CummulativeQuoteQty), 64)
		if err != nil {
			return ExecutionDetail{}, err
		}
		var avgRate float64
		if filledBTC > 0 {
			avgRate = filledFiat / filledBTC
		}
		return ExecutionDetail{
			FilledBTC:   filledBTC,
			AverageRate: avgRate,
			FilledFiat:  filledFiat,
			OrderStatus: status,
		}, nil
	}

	var filledBTC, filledFiat float64
	feesByAsset := map[string]float64{}
	for _, fill := range order.Fills {
		price, err := strconv.ParseFloat(strings.TrimSpace(fill.Price), 64)
		if err != nil {
			return ExecutionDetail{}, err
		}
		qty, err := strconv.ParseFloat(strings.TrimSpace(fill.Qty), 64)
		if err != nil {
			return ExecutionDetail{}, err
		}
		commission, err := strconv.ParseFloat(strings.TrimSpace(fill.Commission), 64)
		if err != nil {
			return ExecutionDetail{}, err
		}
		filledBTC += qty
		filledFiat += price * qty
		if commission != 0 {
			asset := strings.ToUpper(strings.TrimSpace(fill.CommissionAsset))
			feesByAsset[asset] += commission
		}
	}

	var avgRate float64
	if filledBTC > 0 {
		avgRate = filledFiat / filledBTC
	}
	return ExecutionDetail{
		FilledBTC:   filledBTC,
		AverageRate: avgRate,
		FilledFiat:  filledFiat,
		Fees:        feeLinesFromMap(feesByAsset),
		OrderStatus: status,
	}, nil
}

// executionFromCoinbase translates a Coinbase GetOrder result into the common
// ExecutionDetail schema. quoteAsset labels the fee line, since Coinbase's total_fees
// is unlabeled (always the quote currency for a market_market_ioc spot buy). FilledFiat
// is recomputed from rate*size rather than read from the version-dependent filled_value
// field.
func executionFromCoinbase(order coinbase.Order, quoteAsset string) (ExecutionDetail, error) {
	avgRate, err := parseCoinbaseFloat(order.AverageFilledPrice)
	if err != nil {
		return ExecutionDetail{}, err
	}
	filledBTC, err := parseCoinbaseFloat(order.FilledSize)
	if err != nil {
		return ExecutionDetail{}, err
	}
	fee, err := parseCoinbaseFloat(order.TotalFees)
	if err != nil {
		return ExecutionDetail{}, err
	}

	exec := ExecutionDetail{
		FilledBTC:   filledBTC,
		AverageRate: avgRate,
		FilledFiat:  avgRate * filledBTC,
		OrderStatus: normalizeOrderStatus(order.Status),
	}
	if fee > 0 {
		exec.Fees = []FeeLine{{Asset: quoteAsset, Amount: fee}}
	}
	return exec, nil
}

// parseCoinbaseFloat parses one of Coinbase's decimal-string numeric fields, treating
// an empty string as zero.
func parseCoinbaseFloat(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}

// feeLinesFromMap turns an asset->amount fee total map into a sorted []FeeLine, for
// deterministic output (test and UI rendering both benefit from stable ordering).
func feeLinesFromMap(feesByAsset map[string]float64) []FeeLine {
	if len(feesByAsset) == 0 {
		return nil
	}
	assets := make([]string, 0, len(feesByAsset))
	for asset := range feesByAsset {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	lines := make([]FeeLine, 0, len(assets))
	for _, asset := range assets {
		lines = append(lines, FeeLine{Asset: asset, Amount: feesByAsset[asset]})
	}
	return lines
}

const defaultMaxHedgeBTC = 1.0

func maxHedgeBTC() float64 {
	raw := strings.TrimSpace(os.Getenv("MAX_HEDGE_BTC"))
	if raw == "" {
		return defaultMaxHedgeBTC
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return defaultMaxHedgeBTC
	}
	return value
}

// HedgeOpenResult is the normalized result of opening a perpetual hedge position.
type HedgeOpenResult struct {
	Venue       string          `json:"venue"`
	Instrument  string          `json:"instrument"`
	Side        string          `json:"side"`
	BTCNotional float64         `json:"btc_notional"`
	OrderID     string          `json:"order_id"`
	Status      string          `json:"status"`
	SubmittedAt string          `json:"submitted_at"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

var supportedHedgeVenues = map[string]bool{
	"kraken_futures":   true,
	"coinbase_futures": true,
	"binance_futures":  true,
}

// OpenHedge opens a perpetual hedge position on the given futures venue.
// venue must be one of "kraken_futures", "coinbase_futures", "binance_futures".
// side must be "short" or "long". btcNotional is the BTC amount to hedge.
func (s *Service) OpenHedge(ctx context.Context, venue, side string, btcNotional, leverage float64) (*HedgeOpenResult, error) {
	venue = strings.ToLower(strings.TrimSpace(venue))
	side = strings.ToLower(strings.TrimSpace(side))

	if !supportedHedgeVenues[venue] {
		return nil, fmt.Errorf("unsupported hedge venue %q; supported: kraken_futures, coinbase_futures, binance_futures", venue)
	}
	if side != "short" && side != "long" {
		return nil, fmt.Errorf("side must be %q or %q, got %q", "short", "long", side)
	}
	if btcNotional <= 0 {
		return nil, fmt.Errorf("btc_notional must be greater than zero")
	}
	if cap := maxHedgeBTC(); btcNotional > cap {
		return nil, fmt.Errorf("btc_notional %.8f exceeds the configured per-order cap of %.8f BTC", btcNotional, cap)
	}
	key, err := s.futuresKey(ctx, venue)
	if err != nil {
		return nil, err
	}

	result := &HedgeOpenResult{
		Venue:       venue,
		Side:        side,
		BTCNotional: btcNotional,
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}

	switch venue {
	case "kraken_futures":
		client := kraken.NewFuturesClient(key.APIKey, key.APISecret)
		order, err := client.PlaceOrder(ctx, "PI_XBTUSD", side, btcNotional)
		if err != nil {
			return nil, err
		}
		result.Instrument = order.Instrument
		result.OrderID = order.OrderID
		result.Status = order.Status
		result.Raw = order.Raw

	case "coinbase_futures":
		client := coinbase.NewFuturesClient(key.APIKey, key.APISecret, key.Passphrase)
		order, err := client.PlaceOrder(ctx, "BTC-PERP", side, btcNotional)
		if err != nil {
			return nil, err
		}
		result.Instrument = order.Instrument
		result.OrderID = order.OrderID
		result.Status = order.Status
		result.Raw = order.Raw

	case "binance_futures":
		client := binance.NewFuturesClient(key.APIKey, key.APISecret)
		lev := int(leverage)
		if lev <= 0 {
			lev = 1
		}
		order, err := client.PlaceOrder(ctx, "BTCUSDT", side, btcNotional, lev)
		if err != nil {
			return nil, err
		}
		result.Instrument = order.Instrument
		result.OrderID = order.OrderID
		result.Status = order.Status
		result.Raw = order.Raw
	}

	return result, nil
}

// HedgeSnapshot is the live, exchange-sourced state of the hedge instrument on a
// venue. Unlike HedgePositions (which reads our own DB), this reflects the venue's
// own books — so it can detect a position our records don't yet know about.
type HedgeSnapshot struct {
	Venue   string  `json:"venue"`
	Side    string  `json:"side"` // long|short|flat
	SizeBTC float64 `json:"size_btc"`
}

// LiveHedgePosition queries the venue directly for the current hedge position.
// It is the idempotency guard for OpenHedge retries: after a hedge attempt errors,
// callers compare the live position against what they tried to open to decide
// whether the order actually filled (don't retry) or not (safe to retry).
func (s *Service) LiveHedgePosition(ctx context.Context, venue string) (*HedgeSnapshot, error) {
	venue = strings.ToLower(strings.TrimSpace(venue))
	if !supportedHedgeVenues[venue] {
		return nil, fmt.Errorf("unsupported hedge venue %q", venue)
	}
	key, err := s.futuresKey(ctx, venue)
	if err != nil {
		return nil, err
	}

	var (
		update   cex.HedgePositionUpdate
		fetchErr error
	)
	switch venue {
	case "kraken_futures":
		update, fetchErr = kraken.NewFuturesClient(key.APIKey, key.APISecret).CurrentPosition(ctx)
	case "coinbase_futures":
		update, fetchErr = coinbase.NewFuturesClient(key.APIKey, key.APISecret, key.Passphrase).CurrentPosition(ctx)
	case "binance_futures":
		update, fetchErr = binance.NewFuturesClient(key.APIKey, key.APISecret).CurrentPosition(ctx)
	}
	if fetchErr != nil {
		return nil, fetchErr
	}
	side := strings.ToLower(strings.TrimSpace(update.Side))
	if side == "" {
		side = "flat"
	}
	return &HedgeSnapshot{Venue: venue, Side: side, SizeBTC: update.SizeBTC}, nil
}

// StreamHedgePositions starts a venue position stream using the active
// database credential. Credentials are resolved each time a stream starts.
func (s *Service) StreamHedgePositions(ctx context.Context, venue string) (<-chan cex.HedgePositionUpdate, error) {
	venue = strings.ToLower(strings.TrimSpace(venue))
	if !supportedHedgeVenues[venue] {
		return nil, fmt.Errorf("unsupported hedge venue %q", venue)
	}
	key, err := s.futuresKey(ctx, venue)
	if err != nil {
		return nil, err
	}
	switch venue {
	case "kraken_futures":
		return kraken.NewFuturesClient(key.APIKey, key.APISecret).StreamPositions(ctx)
	case "coinbase_futures":
		return coinbase.NewFuturesClient(key.APIKey, key.APISecret, key.Passphrase).StreamPositions(ctx)
	case "binance_futures":
		return binance.NewFuturesClient(key.APIKey, key.APISecret).StreamPositions(ctx)
	default:
		return nil, fmt.Errorf("unsupported hedge venue %q", venue)
	}
}

func (s *Service) futuresKey(ctx context.Context, venue string) (db.APIKey, error) {
	switch venue {
	case "kraken_futures":
		_, key, err := s.activeKeyPreferringPermissions(ctx, "Kraken", false, PermHedgePositions)
		return key, err
	case "binance_futures":
		_, key, err := s.firstActiveKey(ctx, "Binance")
		return key, err
	case "coinbase_futures":
		return s.coinbaseFuturesKey(ctx)
	default:
		return db.APIKey{}, fmt.Errorf("unsupported hedge venue %q", venue)
	}
}

// coinbaseFuturesKey prefers an INTX passphrase key that granted hedge_positions on
// the last health check, then any passphrase key, matching Kraken's dual-key model.
func (s *Service) coinbaseFuturesKey(ctx context.Context) (db.APIKey, error) {
	keys, err := s.repo.GetActiveAPIKeysForProvider(ctx, "Coinbase")
	if err != nil {
		return db.APIKey{}, err
	}
	var fallback db.APIKey
	foundFallback := false
	for _, key := range keys {
		if strings.TrimSpace(key.APIKey) == "" || strings.TrimSpace(key.APISecret) == "" {
			continue
		}
		if strings.TrimSpace(key.Passphrase) == "" {
			continue
		}
		if !foundFallback {
			fallback = key
			foundFallback = true
		}
		if keyGrantsPermission(key, PermHedgePositions) {
			return key, nil
		}
	}
	if foundFallback {
		return fallback, nil
	}
	return db.APIKey{}, fmt.Errorf("no Coinbase International (INTX) API key (key+secret+passphrase) configured for Coinbase")
}

func (s *Service) firstActiveKey(ctx context.Context, rawProvider string) (string, db.APIKey, error) {
	return s.activeKeyPreferringPermissions(ctx, rawProvider, false)
}

// spotAPIKey returns credentials suitable for Kraken/Binance/Coinbase spot endpoints.
// For Kraken, prefers keys that granted a spot permission on the last health check and
// skips futures-only keys so hedge credentials are not used for Balance/Withdraw.
func (s *Service) spotAPIKey(ctx context.Context, rawProvider string) (string, db.APIKey, error) {
	return s.activeKeyPreferringPermissions(ctx, rawProvider, true,
		PermFetchDepositAddress, PermSpotTrading, PermReadBalances, PermWithdraw)
}

func (s *Service) activeKeyPreferringPermissions(ctx context.Context, rawProvider string, skipKrakenFuturesOnly bool, preferred ...string) (string, db.APIKey, error) {
	provider, err := CanonicalProvider(rawProvider)
	if err != nil {
		return "", db.APIKey{}, err
	}
	keys, err := s.repo.GetActiveAPIKeysForProvider(ctx, provider)
	if err != nil {
		return "", db.APIKey{}, err
	}
	if len(keys) == 0 {
		return "", db.APIKey{}, fmt.Errorf("no active API keys configured for %s", provider)
	}

	var fallback db.APIKey
	foundFallback := false
	for _, key := range keys {
		if strings.TrimSpace(key.APIKey) == "" || strings.TrimSpace(key.APISecret) == "" {
			continue
		}
		if skipKrakenFuturesOnly && provider == "Kraken" && isKrakenFuturesOnlyKey(key) {
			continue
		}
		if !foundFallback {
			fallback = key
			foundFallback = true
		}
		if len(preferred) == 0 {
			return provider, key, nil
		}
		for _, perm := range preferred {
			if keyGrantsPermission(key, perm) {
				return provider, key, nil
			}
		}
	}
	if foundFallback {
		return provider, fallback, nil
	}
	if skipKrakenFuturesOnly && provider == "Kraken" {
		// Only futures-only keys remain; fall back so callers still get a clear API error.
		for _, key := range keys {
			if strings.TrimSpace(key.APIKey) != "" && strings.TrimSpace(key.APISecret) != "" {
				return provider, key, nil
			}
		}
	}
	return "", db.APIKey{}, fmt.Errorf("active %s API key is missing credentials", provider)
}

func keyPermissionsMap(key db.APIKey) map[string]string {
	raw := bytes.TrimSpace(key.Permissions)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var perms map[string]string
	if err := json.Unmarshal(raw, &perms); err != nil {
		return nil
	}
	return perms
}

func keyGrantsPermission(key db.APIKey, permission string) bool {
	return keyPermissionsMap(key)[permission] == PermGranted
}

func isKrakenFuturesOnlyKey(key db.APIKey) bool {
	perms := keyPermissionsMap(key)
	if perms == nil || perms[PermHedgePositions] != PermGranted {
		return false
	}
	for _, perm := range []string{PermFetchDepositAddress, PermSpotTrading, PermReadBalances, PermWithdraw} {
		if perms[perm] == PermGranted {
			return false
		}
	}
	return true
}

// firstActiveKeyWithPassphrase returns the first active key that carries a passphrase.
// Coinbase Exchange (address book) credentials are key+secret+passphrase, distinct from
// the CDP/Advanced Trade keys (no passphrase) used for deposits and balances.
func (s *Service) firstActiveKeyWithPassphrase(ctx context.Context, rawProvider string) (string, db.APIKey, error) {
	provider, err := CanonicalProvider(rawProvider)
	if err != nil {
		return "", db.APIKey{}, err
	}
	keys, err := s.repo.GetActiveAPIKeysForProvider(ctx, provider)
	if err != nil {
		return "", db.APIKey{}, err
	}
	for _, key := range keys {
		if strings.TrimSpace(key.APIKey) == "" || strings.TrimSpace(key.APISecret) == "" {
			continue
		}
		if strings.TrimSpace(key.Passphrase) == "" {
			continue
		}
		return provider, key, nil
	}
	return "", db.APIKey{}, fmt.Errorf("no Coinbase Exchange API key (key+secret+passphrase) configured for %s", provider)
}

func balancesFromBinance(balances []binance.SpotBalance) *BalancesResponse {
	resp := &BalancesResponse{
		Fiat:   []AssetBalance{},
		Source: "binance",
	}
	for _, row := range balances {
		available, err := strconv.ParseFloat(strings.TrimSpace(row.Free), 64)
		if err != nil {
			continue
		}
		locked, err := strconv.ParseFloat(strings.TrimSpace(row.Locked), 64)
		if err != nil {
			locked = 0
		}
		total := available + locked
		if total <= 0 {
			continue
		}
		asset := strings.ToUpper(strings.TrimSpace(row.Asset))
		entry := AssetBalance{
			Asset:     asset,
			Available: available,
			Total:     total,
		}
		if isBitcoinAsset(asset) {
			copy := entry
			resp.Bitcoin = &copy
			continue
		}
		if isFiatAsset(asset) {
			resp.Fiat = append(resp.Fiat, entry)
		}
	}
	return resp
}

func balancesFromCoinbase(accounts []coinbase.Account) *BalancesResponse {
	resp := &BalancesResponse{
		Fiat:   []AssetBalance{},
		Source: "coinbase",
	}
	for _, account := range accounts {
		if !account.Active {
			continue
		}
		availableRaw := strings.TrimSpace(account.AvailableBalance.Value)
		available := 0.0
		if availableRaw != "" {
			parsed, err := strconv.ParseFloat(availableRaw, 64)
			if err != nil {
				continue
			}
			available = parsed
		}
		held, err := strconv.ParseFloat(strings.TrimSpace(account.Hold.Value), 64)
		if err != nil {
			held = 0
		}
		total := available + held
		asset := strings.ToUpper(strings.TrimSpace(account.Currency))
		entry := AssetBalance{
			Asset:     asset,
			Available: available,
			Total:     total,
		}
		if isBitcoinAsset(asset) {
			copy := entry
			resp.Bitcoin = &copy
			continue
		}
		if isFiatAsset(asset) {
			resp.Fiat = append(resp.Fiat, entry)
		}
	}
	return resp
}

func balancesFromKraken(balances kraken.Balance) *BalancesResponse {
	resp := &BalancesResponse{
		Fiat:   []AssetBalance{},
		Source: "kraken",
	}
	for asset, amountRaw := range balances {
		amount, err := strconv.ParseFloat(amountRaw, 64)
		if err != nil || amount <= 0 {
			continue
		}
		normalized := normalizeKrakenAsset(asset)
		entry := AssetBalance{
			Asset:     normalized,
			Available: amount,
			Total:     amount,
		}
		if isBitcoinAsset(normalized) {
			if resp.Bitcoin == nil || entry.Total > resp.Bitcoin.Total {
				copy := entry
				resp.Bitcoin = &copy
			}
			continue
		}
		if isFiatAsset(normalized) {
			resp.Fiat = append(resp.Fiat, entry)
		}
	}
	return resp
}

func hedgePositionFromTransaction(tx db.Transaction) HedgePosition {
	position := HedgePosition{
		TransactionID: tx.ID,
		Status:        string(tx.Status),
		Direction:     tx.Direction,
		Venue:         string(tx.CexHedgingPosition),
		SizeBTC:       tx.CexBitcoinAmount,
		Fee:           tx.CexCounterPositionFee,
		OpenDetails:   tx.CexCounterPositionOpenDetails,
		CloseDetails:  tx.CexCounterPositionCloseDetails,
	}
	if !tx.CreatedAt.IsZero() {
		position.OpenedAt = tx.CreatedAt.UTC().Format(time.RFC3339)
	}
	if tx.TransactionComplete && !tx.UpdatedAt.IsZero() {
		position.ClosedAt = tx.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if hasJSONData(tx.CexCounterPositionOpenDetails) {
		var details map[string]any
		if err := json.Unmarshal(tx.CexCounterPositionOpenDetails, &details); err == nil {
			if instrument, ok := details["instrument"].(string); ok {
				position.Instrument = instrument
			}
			if venue, ok := details["venue"].(string); ok && position.Venue == "" {
				position.Venue = venue
			}
		}
	}
	return position
}

func hasJSONData(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func normalizeKrakenAsset(asset string) string {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "XXBT", "XBT":
		return "BTC"
	case "ZEUR":
		return "EUR"
	case "ZUSD":
		return "USD"
	case "ZGBP":
		return "GBP"
	case "ZCHF":
		return "CHF"
	default:
		return strings.TrimPrefix(strings.ToUpper(asset), "Z")
	}
}

func isBitcoinAsset(asset string) bool {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "BTC", "XBT", "XXBT":
		return true
	default:
		return false
	}
}

// WhitelistedLNDWithdrawAddress returns the first on-chain address that appears in
// both the LND wallet and the CEX's whitelisted withdrawal addresses. This is the
// address that must be used for the CEX withdrawal — a freshly generated address
// would be unknown to the CEX whitelist and the withdrawal would be rejected.
func (s *Service) WhitelistedLNDWithdrawAddress(ctx context.Context, rawProvider string) (string, error) {
	if s.lnd == nil {
		return "", fmt.Errorf("LND wallet not available; cannot determine whitelisted withdrawal address")
	}
	provider, err := CanonicalProvider(rawProvider)
	if err != nil {
		return "", err
	}

	walletProps, err := s.lnd.ListWalletAddresses(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching LND wallet addresses: %w", err)
	}
	walletAddrs := make([]string, len(walletProps))
	for i, p := range walletProps {
		walletAddrs[i] = p.Address
	}

	addrs, err := s.WithdrawalAddresses(ctx, provider)
	if err != nil {
		return "", fmt.Errorf("fetching %s withdrawal whitelist: %w", provider, err)
	}
	cexAddrs := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Whitelisted {
			cexAddrs = append(cexAddrs, a.Address)
		}
	}
	if matched := MatchWhitelistedLNDAddress(walletAddrs, cexAddrs); matched != "" {
		return matched, nil
	}
	return "", fmt.Errorf(
		"no whitelisted %s withdrawal address matches the current LND wallet; add an LND on-chain address to the %s withdrawal whitelist before trading",
		provider, provider,
	)
}

// WithdrawSpotToLND initiates an on-chain BTC withdrawal from the CEX to the given
// LND wallet address. lndAddress must already be on the CEX's withdrawal whitelist —
// use WhitelistedLNDWithdrawAddress to obtain it. amountBTC is the net BTC to
// withdraw (typically SpotOrderResult.Execution.FilledBTC).
func (s *Service) WithdrawSpotToLND(ctx context.Context, rawProvider string, amountBTC float64, lndAddress, clientRequestID string) (*lnd.WithdrawFromExchangeResult, error) {
	if amountBTC <= 0 {
		return nil, fmt.Errorf("withdraw amount must be greater than zero")
	}
	if strings.TrimSpace(lndAddress) == "" {
		return nil, fmt.Errorf("lnd address is required")
	}
	if s.lnd == nil {
		return nil, fmt.Errorf("LND wallet not available")
	}

	provider, key, err := s.spotAPIKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}

	amount := strconv.FormatFloat(amountBTC, 'f', 8, 64)
	// A stable, caller-supplied key makes the withdrawal idempotent across retries
	// (Binance keys on withdrawOrderId). Fall back to a time-based id for ad-hoc callers.
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		clientRequestID = fmt.Sprintf("tradeflow-%s-%d", provider, time.Now().UnixMilli())
	}

	switch strings.ToLower(provider) {
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		bridge := lnd.NewWalletExchangeBridge(s.lnd, lnd.NewBinanceBridge(client))
		return bridge.WithdrawFromExchangeToLND(ctx, lnd.WithdrawFromExchangeRequest{
			Coin:            "BTC",
			Network:         "BTC",
			Amount:          amount,
			LNDDestination:  lndAddress,
			ClientRequestID: clientRequestID,
		})
	case "kraken":
		client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
		rows, err := client.GetWithdrawAddresses(ctx, "XBT")
		if err != nil {
			return nil, fmt.Errorf("kraken: list withdrawal addresses: %w", err)
		}
		withdrawKey, err := krakenWithdrawKeyForAddress(rows, lndAddress)
		if err != nil {
			return nil, err
		}
		bridge := lnd.NewWalletExchangeBridge(s.lnd, lnd.NewKrakenBridge(
			client,
			map[string]string{"XBT": "Bitcoin"},
			nil,
			nil,
			nil,
		))
		return bridge.WithdrawFromExchangeToLND(ctx, lnd.WithdrawFromExchangeRequest{
			Coin:            "XBT",
			Network:         "Bitcoin",
			Amount:          amount,
			LNDDestination:  lndAddress,
			ClientRequestID: clientRequestID,
			AddressTag:      withdrawKey,
		})
	default:
		return nil, fmt.Errorf("CEX→LND withdrawal not yet supported for %s", provider)
	}
}

func krakenWithdrawKeyForAddress(rows []kraken.WithdrawAddress, lndAddress string) (string, error) {
	target := strings.ToLower(strings.TrimSpace(lndAddress))
	if target == "" {
		return "", fmt.Errorf("kraken: lnd address is required")
	}
	var key string
	var matches int
	for _, row := range rows {
		if !row.Verified || strings.TrimSpace(row.Address) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.Address), target) {
			matches++
			key = strings.TrimSpace(row.Key)
		}
	}
	if matches == 0 {
		return "", fmt.Errorf("kraken: no verified withdrawal address matches %q", lndAddress)
	}
	if matches > 1 {
		return "", fmt.Errorf("kraken: ambiguous withdrawal keys for address %q", lndAddress)
	}
	if key == "" {
		return "", fmt.Errorf("kraken: withdrawal key missing for address %q", lndAddress)
	}
	return key, nil
}

func isFiatAsset(asset string) bool {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "EUR", "USD", "GBP", "CHF", "USDT", "USDC", "CAD", "AUD", "JPY":
		return true
	default:
		return false
	}
}
