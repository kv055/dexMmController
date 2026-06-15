package cexaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/cex/binance"
	"dexMmController/internal/exchanges/cex/coinbase"
	"dexMmController/internal/exchanges/cex/coinbaseexchange"
	"dexMmController/internal/exchanges/cex/kraken"
	"dexMmController/internal/hummingbot"
)

// Repository loads account credentials for CEX operations.
type Repository interface {
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	GetTransactionsByCEX(cexName string) ([]db.Transaction, error)
}

// Service fetches live CEX account data for the trading UI.
type Service struct {
	repo Repository
	hb   *hummingbot.Client
	cfg  accounthealth.Config
}

// NewService creates a CEX account service.
func NewService(repo Repository, hb *hummingbot.Client, cfg accounthealth.Config) *Service {
	return &Service{repo: repo, hb: hb, cfg: cfg}
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
	Provider     string          `json:"provider"`
	Pair         string          `json:"pair"`
	Side         string          `json:"side"`
	RequestedBTC float64         `json:"requested_btc"`
	OrderID      string          `json:"order_id"`
	Status       string          `json:"status"`
	SubmittedAt  string          `json:"submitted_at"`
	Raw          json.RawMessage `json:"raw,omitempty"`
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
	provider, key, err := s.firstActiveKey(ctx, rawProvider)
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
	provider, key, err := s.firstActiveKey(ctx, rawProvider)
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
	provider, key, err := s.firstActiveKey(ctx, rawProvider)
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
		if s.hb == nil {
			return nil, errors.New("hummingbot client is not configured")
		}
		return balancesFromHummingbot(ctx, s.hb, provider)
	}
}

// HedgePositions returns active and past hedge positions from MM transactions.
func (s *Service) HedgePositions(ctx context.Context, rawProvider string) (*HedgePositionsResponse, error) {
	provider, err := accounthealth.CanonicalProvider(rawProvider)
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
func (s *Service) SpotTrades(ctx context.Context, rawProvider string, limit int) ([]SpotTrade, error) {
	provider, err := accounthealth.CanonicalProvider(rawProvider)
	if err != nil {
		return nil, err
	}
	if s.hb == nil {
		return nil, errors.New("hummingbot client is not configured")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	trades, err := s.hb.Trades(ctx, hummingbot.TradeFilterRequest{
		ConnectorNames: []string{provider},
		Limit:          limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]SpotTrade, 0, len(trades))
	for _, trade := range trades {
		out = append(out, SpotTrade{
			TradeID:     trade.TradeID,
			TradingPair: trade.TradingPair,
			TradeType:   trade.TradeType,
			Price:       trade.Price,
			Amount:      trade.Amount,
			Fee:         trade.Fee,
			FeeAsset:    trade.FeeCurrency,
			Timestamp:   time.UnixMilli(trade.Timestamp).UTC().Format(time.RFC3339),
		})
	}
	return out, nil
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

// marketBuyPair builds the exchange-native trading pair for buying BTC with fiatCurrency.
func marketBuyPair(provider, fiatCurrency string) string {
	fiat := strings.ToUpper(strings.TrimSpace(fiatCurrency))
	if fiat == "" {
		fiat = "EUR"
	}
	switch strings.ToLower(provider) {
	case "kraken":
		return "XBT" + fiat
	case "coinbase":
		return "BTC-" + fiat
	default: // binance and others
		return "BTC" + fiat
	}
}

// MarketBuyBTC places a market buy order for btcAmount of BTC on the given CEX provider,
// pairing BTC against fiatCurrency. It is the trade-tracing "Start trade" action.
func (s *Service) MarketBuyBTC(ctx context.Context, rawProvider string, btcAmount float64, fiatCurrency string) (*SpotOrderResult, error) {
	if btcAmount <= 0 {
		return nil, fmt.Errorf("btc_amount must be greater than zero")
	}
	if cap := maxMarketBuyBTC(); btcAmount > cap {
		return nil, fmt.Errorf("btc_amount %.8f exceeds the configured per-order cap of %.8f BTC", btcAmount, cap)
	}

	provider, key, err := s.firstActiveKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}
	pair := marketBuyPair(provider, fiatCurrency)
	result := &SpotOrderResult{
		Provider:     provider,
		Pair:         pair,
		Side:         "BUY",
		RequestedBTC: btcAmount,
		SubmittedAt:  time.Now().UTC().Format(time.RFC3339),
	}

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
	case "binance":
		client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
		order, err := client.MarketBuy(ctx, pair, btcAmount)
		if err != nil {
			return nil, err
		}
		result.OrderID = strconv.FormatInt(order.OrderID, 10)
		result.Status = strings.ToLower(order.Status)
		result.Raw, _ = json.Marshal(order)
	case "coinbase":
		client := coinbase.NewClient(s.cfg.CoinbaseBaseURL, key.APIKey, key.APISecret)
		order, err := client.MarketBuy(ctx, pair, btcAmount)
		if err != nil {
			return nil, err
		}
		result.OrderID = order.SuccessResponse.OrderID
		result.Status = "submitted"
		result.Raw, _ = json.Marshal(order)
	default:
		return nil, fmt.Errorf("market buy is not supported for %s yet", provider)
	}

	return result, nil
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

	result := &HedgeOpenResult{
		Venue:       venue,
		Side:        side,
		BTCNotional: btcNotional,
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}

	switch venue {
	case "kraken_futures":
		client := kraken.NewFuturesClient()
		order, err := client.PlaceOrder(ctx, "PI_XBTUSD", side, btcNotional)
		if err != nil {
			return nil, err
		}
		result.Instrument = order.Instrument
		result.OrderID = order.OrderID
		result.Status = order.Status
		result.Raw = order.Raw

	case "coinbase_futures":
		client := coinbase.NewFuturesClient()
		order, err := client.PlaceOrder(ctx, "BTC-PERP", side, btcNotional)
		if err != nil {
			return nil, err
		}
		result.Instrument = order.Instrument
		result.OrderID = order.OrderID
		result.Status = order.Status
		result.Raw = order.Raw

	case "binance_futures":
		client := binance.NewFuturesClient()
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

func (s *Service) firstActiveKey(ctx context.Context, rawProvider string) (string, db.APIKey, error) {
	provider, err := accounthealth.CanonicalProvider(rawProvider)
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
	key := keys[0]
	if strings.TrimSpace(key.APIKey) == "" || strings.TrimSpace(key.APISecret) == "" {
		return "", db.APIKey{}, fmt.Errorf("active %s API key is missing credentials", provider)
	}
	return provider, key, nil
}

// firstActiveKeyWithPassphrase returns the first active key that carries a passphrase.
// Coinbase Exchange (address book) credentials are key+secret+passphrase, distinct from
// the CDP/Advanced Trade keys (no passphrase) used for deposits and balances.
func (s *Service) firstActiveKeyWithPassphrase(ctx context.Context, rawProvider string) (string, db.APIKey, error) {
	provider, err := accounthealth.CanonicalProvider(rawProvider)
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
		available, err := strconv.ParseFloat(strings.TrimSpace(account.AvailableBalance.Value), 64)
		if err != nil {
			continue
		}
		held, err := strconv.ParseFloat(strings.TrimSpace(account.Hold.Value), 64)
		if err != nil {
			held = 0
		}
		total := available + held
		if total <= 0 && available <= 0 {
			continue
		}
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

func balancesFromHummingbot(ctx context.Context, client *hummingbot.Client, provider string) (*BalancesResponse, error) {
	state, err := client.PortfolioState(ctx, hummingbot.PortfolioStateRequest{
		ConnectorNames: []string{provider},
		SkipGateway:    true,
		Refresh:        true,
	})
	if err != nil {
		return nil, err
	}

	resp := &BalancesResponse{
		Fiat:   []AssetBalance{},
		Source: "hummingbot",
	}
	for _, connectors := range state {
		for connName, tokens := range connectors {
			if !strings.EqualFold(connName, provider) {
				continue
			}
			for _, token := range tokens {
				if token.Total <= 0 && token.Available <= 0 {
					continue
				}
				entry := AssetBalance{
					Asset:     token.Token,
					Available: token.Available,
					Total:     token.Total,
					USDValue:  token.USDValue,
				}
				if isBitcoinAsset(token.Token) {
					copy := entry
					resp.Bitcoin = &copy
					continue
				}
				if isFiatAsset(token.Token) {
					resp.Fiat = append(resp.Fiat, entry)
				}
			}
		}
	}
	return resp, nil
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

func isFiatAsset(asset string) bool {
	switch strings.ToUpper(strings.TrimSpace(asset)) {
	case "EUR", "USD", "GBP", "CHF", "USDT", "USDC", "CAD", "AUD", "JPY":
		return true
	default:
		return false
	}
}
