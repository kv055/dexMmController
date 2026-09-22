package bisqexec

import (
	"commons/bisqroute"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/pretrade"
	"dexMmController/internal/tradeflow"
)

const (
	quoteTTL            = 60 * time.Second
	acceptedDeadline    = 30 * time.Minute
	hedgelessOnChainFee = int64(400) // reserved LND miner fee, not Bisq protocol funds
)

var (
	ErrAutomatedDisabled = errors.New("automated Bisq execution is not ready")
	ErrQuoteExpired      = errors.New("bisq quote has expired")
	ErrIdempotencyReuse  = errors.New("idempotency key was reused with different parameters")
)

// Daemon is the Bisq v1 connector subset used for quoting and preflight.
type Daemon interface {
	RequireGuardVersion(context.Context) error
	QuoteTakeOffer(context.Context, string, bisq.QuoteTakeOfferParams) (*bisq.TakeOfferQuote, error)
	GetPaymentAccounts(context.Context) ([]bisq.PaymentAccount, error)
	RequireFundingAddress(context.Context, string) error
}

type Store interface {
	GetBisqPaymentAccountMapping(ctx context.Context, method, currency string) (*db.BisqPaymentAccountMapping, error)
	ActiveBisqFundingDestination(ctx context.Context) (*db.BisqFundingDestination, error)
	CreateBisqExecutionQuote(ctx context.Context, quote *db.BisqExecutionQuote) error
	GetBisqExecutionQuote(ctx context.Context, id string) (*db.BisqExecutionQuote, error)
	GetBisqExecutionQuoteByIdempotency(ctx context.Context, key string) (*db.BisqExecutionQuote, error)
	GetTransaction(id uint) (*db.Transaction, error)
}

type WhitelistFetcher interface {
	WhitelistedBTCAddresses(ctx context.Context, provider string) ([]string, error)
}

type Runner interface {
	Run(ctx context.Context, order tradeflow.Order) (*tradeflow.Result, error)
}

// QuoteRequest is the operator-facing quote input.
type QuoteRequest struct {
	OfferID            string
	BTCAmountSats      uint64
	TradeMode          string
	PaymentMethod      string
	PaymentMethods     []string
	FiatCurrency       string
	Provider           string
	ResolvedRoute      *db.ResolvedRoute
	SelectedOffer      json.RawMessage
	MaxCexFiatSpend    float64
	MaxWalletDebitSats uint64
	MinFiatProceeds    float64
}

// QuoteResult is returned to the UI for confirmation.
type QuoteResult struct {
	QuoteID             string                `json:"quote_id"`
	ExpiresAt           time.Time             `json:"expires_at"`
	TradeMode           string                `json:"trade_mode"`
	PaymentAccountID    string                `json:"payment_account_id"`
	FundingAddress      string                `json:"funding_address"`
	TradeAmountSats     uint64                `json:"trade_amount_sats"`
	SecurityDepositSats uint64                `json:"security_deposit_sats"`
	TakerFeeSats        uint64                `json:"taker_fee_sats"`
	MiningFeeSats       uint64                `json:"mining_fee_sats"`
	TotalToPaySats      uint64                `json:"total_to_pay_sats"`
	HedgelessFeeSats    int64                 `json:"hedgeless_network_fee_sats,omitempty"`
	FiatAmount          float64               `json:"fiat_amount"`
	Volume              string                `json:"volume"`
	Limits              db.BisqSpendingLimits `json:"limits"`
	IsBtcWalletFunded   bool                  `json:"is_btc_wallet_funded"`
	MissingSats         uint64                `json:"missing_sats"`
}

// AcceptRequest binds a stored quote to an idempotent execution.
type AcceptRequest struct {
	QuoteID        string
	IdempotencyKey string
}

// AcceptResult is the HTTP 202 payload.
type AcceptResult struct {
	TransactionID uint   `json:"transaction_id"`
	Accepted      bool   `json:"accepted"`
	Stage         string `json:"stage"`
	TradeMode     string `json:"trade_mode"`
}

// Service quotes, accepts, and runs durable Bisq v1 bid execution.
type Service struct {
	daemon      Daemon
	store       Store
	whitelist   WhitelistFetcher
	runner      Runner
	now         func() time.Time
	enqueue     func(uint)
	enabled     bool
	routeQuoter RouteQuoter
}

func New(daemon Daemon, store Store, whitelist WhitelistFetcher, runner Runner) *Service {
	svc := &Service{daemon: daemon, store: store, whitelist: whitelist, runner: runner, now: time.Now}
	return svc
}

// Enabled is a deployment gate, not a bypass for connector readiness checks.
func (s *Service) WithEnabled(enabled bool) *Service { s.enabled = enabled; return s }

func (s *Service) WithNow(now func() time.Time) *Service {
	s.now = now
	return s
}

func (s *Service) WithEnqueue(fn func(uint)) *Service {
	s.enqueue = fn
	return s
}

func (s *Service) Quote(ctx context.Context, req QuoteRequest) (*QuoteResult, error) {
	if s.daemon == nil || s.store == nil {
		return nil, ErrAutomatedDisabled
	}
	if err := s.daemon.RequireGuardVersion(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAutomatedDisabled, err)
	}
	if req.ResolvedRoute == nil {
		return nil, fmt.Errorf("resolved_route is required")
	}
	if err := req.ResolvedRoute.ValidateAuthoritativeSnapshot(); err != nil {
		return nil, fmt.Errorf("invalid resolved_route: %w", err)
	}
	if req.ResolvedRoute.TargetDirection != "bid" || req.ResolvedRoute.MarketSide != "bid" ||
		!strings.EqualFold(req.ResolvedRoute.Provider, req.Provider) || req.ResolvedRoute.TradeMode != req.TradeMode {
		return nil, errors.New("Bisq quote requires a matching bid-side route and provider")
	}
	if math.IsNaN(req.MaxCexFiatSpend) || math.IsInf(req.MaxCexFiatSpend, 0) || req.MaxCexFiatSpend < 0 ||
		math.IsNaN(req.MinFiatProceeds) || math.IsInf(req.MinFiatProceeds, 0) || req.MinFiatProceeds < 0 {
		return nil, errors.New("Bisq limits must be finite and non-negative")
	}
	if req.BTCAmountSats == 0 {
		req.BTCAmountSats = uint64(dexaccount.BTCToSats(req.ResolvedRoute.BTCAmount))
	}
	if req.BTCAmountSats == 0 {
		return nil, fmt.Errorf("btc amount is required")
	}
	method := strings.TrimSpace(req.PaymentMethod)
	currency := strings.ToUpper(strings.TrimSpace(req.FiatCurrency))
	if currency == "" {
		currency = strings.ToUpper(strings.TrimSpace(req.ResolvedRoute.FiatCurrency))
	}
	mapping, err := s.store.GetBisqPaymentAccountMapping(ctx, method, currency)
	if err != nil {
		return nil, err
	}
	if mapping == nil || strings.TrimSpace(mapping.BisqPaymentAccountID) == "" {
		return nil, fmt.Errorf("%w: no Bisq payment-account mapping for %s/%s", ErrAutomatedDisabled, method, currency)
	}
	accounts, err := s.daemon.GetPaymentAccounts(ctx)
	if err != nil {
		return nil, err
	}
	account, err := matchPaymentAccount(accounts, mapping.BisqPaymentAccountID, method, currency)
	if err != nil {
		return nil, err
	}
	dest, err := s.store.ActiveBisqFundingDestination(ctx)
	if err != nil {
		return nil, err
	}
	if dest == nil || strings.TrimSpace(dest.Address) == "" {
		return nil, fmt.Errorf("%w: no designated Bisq funding address", ErrAutomatedDisabled)
	}
	if err := s.daemon.RequireFundingAddress(ctx, dest.Address); err != nil {
		return nil, err
	}
	if !strings.EqualFold(req.TradeMode, "hedgeless_double_collateral") {
		if s.whitelist == nil {
			return nil, fmt.Errorf("%w: CEX whitelist is not configured", ErrAutomatedDisabled)
		}
		listed, err := s.whitelist.WhitelistedBTCAddresses(ctx, req.Provider)
		if err != nil {
			return nil, err
		}
		if !pretrade.AddressIsWhitelisted(dest.Address, listed) {
			return nil, fmt.Errorf("%w: designated Bisq address is not approved at %s", ErrAutomatedDisabled, req.Provider)
		}
	}
	daemonQuote, err := s.daemon.QuoteTakeOffer(ctx, strings.TrimSpace(req.OfferID), bisq.QuoteTakeOfferParams{
		PaymentAccountID: account.ID,
		AmountSats:       req.BTCAmountSats,
	})
	if bisq.RPCUnimplemented(err) {
		return nil, fmt.Errorf("%w: daemon is missing QuoteTakeOffer", ErrAutomatedDisabled)
	}
	if err != nil {
		return nil, err
	}
	if daemonQuote == nil || daemonQuote.OfferID != strings.TrimSpace(req.OfferID) || daemonQuote.PaymentAccountID != account.ID ||
		!daemonQuote.IsBuyOffer || !daemonQuote.IsCurrencyForTakerFeeBtc || !strings.EqualFold(daemonQuote.CurrencyCode, currency) ||
		daemonQuote.VolumeAsLong <= 0 || daemonQuote.TotalToPaySats < daemonQuote.TradeAmountSats {
		return nil, errors.New("daemon quote does not match the requested BTC-fee bid trade")
	}
	if daemonQuote.TradeAmountSats != req.BTCAmountSats {
		return nil, fmt.Errorf("daemon trade amount %d does not match requested %d", daemonQuote.TradeAmountSats, req.BTCAmountSats)
	}
	fiat := bisq.FiatAmountFromVolumeAsLong(daemonQuote.VolumeAsLong)
	if s.routeQuoter == nil {
		return nil, fmt.Errorf("%w: authoritative route quoter is not configured", ErrAutomatedDisabled)
	}
	fundingFeeCap := uint64(0)
	if req.TradeMode == "hedgeless_double_collateral" {
		fundingFeeCap = uint64(hedgelessOnChainFee)
	}
	route, err := s.routeQuoter.RequoteBisq(ctx, bisqroute.Request{
		OfferID: req.OfferID, Provider: req.Provider, TradeMode: req.TradeMode, FiatCurrency: currency,
		TradeAmountSats: daemonQuote.TradeAmountSats, TotalToPaySats: daemonQuote.TotalToPaySats,
		SecurityDepositSats: daemonQuote.SecurityDepositSats, TakerFeeSats: daemonQuote.TakerFeeSats,
		MiningFeeSats: daemonQuote.MiningFeeSats, VolumeAsLong: daemonQuote.VolumeAsLong, FundingFeeCapSats: fundingFeeCap,
	})
	if err != nil {
		return nil, err
	}
	if route == nil || route.ValidateAuthoritativeSnapshot() != nil || !strings.EqualFold(route.Provider, req.Provider) || route.TradeMode != req.TradeMode || route.MarketSide != "bid" || route.Settlement != "onchain_direct" ||
		!strings.EqualFold(route.FiatCurrency, currency) || dexaccount.BTCToSats(route.BTCAmount) != int64(daemonQuote.TradeAmountSats) || math.Abs(route.FiatAmount-fiat) > 0.00001 || route.RequiredCapital <= 0 || math.IsNaN(route.RequiredCapital) || math.IsInf(route.RequiredCapital, 0) {
		return nil, errors.New("authoritative route does not match daemon quote")
	}
	limits := db.BisqSpendingLimits{
		MaxFundingFeeSats: fundingFeeCap,
		MaxCexFiatSpend:    req.MaxCexFiatSpend,
		MaxWalletDebitSats: req.MaxWalletDebitSats,
		MinFiatProceeds:    req.MinFiatProceeds,
	}
	if limits.MaxWalletDebitSats == 0 {
		limits.MaxWalletDebitSats = daemonQuote.TotalToPaySats
	}
	if limits.MinFiatProceeds == 0 {
		limits.MinFiatProceeds = fiat
	}
	if limits.MaxCexFiatSpend == 0 {
		limits.MaxCexFiatSpend = route.RequiredCapital
	}
	if daemonQuote.TotalToPaySats > limits.MaxWalletDebitSats {
		return nil, fmt.Errorf("total to pay %d exceeds max wallet debit %d", daemonQuote.TotalToPaySats, limits.MaxWalletDebitSats)
	}
	if fiat+1e-9 < limits.MinFiatProceeds {
		return nil, fmt.Errorf("rounded fiat volume %.2f is below minimum proceeds %.2f", fiat, limits.MinFiatProceeds)
	}
	if route.RequiredCapital-1e-9 > limits.MaxCexFiatSpend {
		return nil, fmt.Errorf("required CEX spend %.2f exceeds max fiat spend %.2f", route.RequiredCapital, limits.MaxCexFiatSpend)
	}
	quoteJSON, _ := json.Marshal(daemonQuote)
	limitsJSON, _ := json.Marshal(limits)
	routeJSON, _ := json.Marshal(route)
	row := &db.BisqExecutionQuote{
		ID:               uuid.NewString(),
		OfferID:          strings.TrimSpace(req.OfferID),
		TradeMode:        strings.TrimSpace(req.TradeMode),
		PaymentAccountID: account.ID,
		LocalAccountID:   &mapping.LocalAccountID,
		Quote:            quoteJSON,
		Limits:           limitsJSON,
		ResolvedRoute:    routeJSON,
		SelectedOffer:    req.SelectedOffer,
		PaymentMethod:    method,
		FundingAddress:   dest.Address,
		ExpiresAt:        s.now().UTC().Add(quoteTTL),
		CreatedAt:        s.now().UTC(),
	}
	if err := s.store.CreateBisqExecutionQuote(ctx, row); err != nil {
		return nil, err
	}
	result := &QuoteResult{
		QuoteID:             row.ID,
		ExpiresAt:           row.ExpiresAt,
		TradeMode:           row.TradeMode,
		PaymentAccountID:    account.ID,
		FundingAddress:      dest.Address,
		TradeAmountSats:     daemonQuote.TradeAmountSats,
		SecurityDepositSats: daemonQuote.SecurityDepositSats,
		TakerFeeSats:        daemonQuote.TakerFeeSats,
		MiningFeeSats:       daemonQuote.MiningFeeSats,
		TotalToPaySats:      daemonQuote.TotalToPaySats,
		FiatAmount:          fiat,
		Volume:              daemonQuote.Volume,
		Limits:              limits,
		IsBtcWalletFunded:   daemonQuote.IsBtcWalletFunded,
		MissingSats:         daemonQuote.MissingSats,
	}
	if strings.EqualFold(row.TradeMode, "hedgeless_double_collateral") {
		result.HedgelessFeeSats = hedgelessOnChainFee
	}
	return result, nil
}

func (s *Service) Accept(ctx context.Context, req AcceptRequest) (*AcceptResult, error) {
	store, durable := s.store.(DurableStore)
	driver, resumable := s.runner.(StepRunner)
	if !durable {
		return nil, ErrAutomatedDisabled
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		return nil, fmt.Errorf("idempotency_key is required")
	}
	existing, err := s.store.GetBisqExecutionQuoteByIdempotency(ctx, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if strings.TrimSpace(existing.ID) != strings.TrimSpace(req.QuoteID) {
			return nil, ErrIdempotencyReuse
		}
		if existing.TransactionID != nil {
			// A retry is a read, including after the deployment gate is disabled.
			// Never present a legacy row without a durable run as resumable.
			run, _, err := store.GetBisqRun(ctx, *existing.TransactionID)
			if err != nil {
				return nil, fmt.Errorf("load accepted execution: %w", err)
			}
			if run.Version != db.BisqExecutionVersion || run.QuoteID != existing.ID || run.RequestKey != key {
				return nil, db.ErrBisqConflict
			}
			return &AcceptResult{TransactionID: run.TransactionID, Accepted: true, Stage: run.Stage, TradeMode: existing.TradeMode}, nil
		}
	}
	if !s.enabled || !resumable {
		return nil, ErrAutomatedDisabled
	}
	quote, err := s.store.GetBisqExecutionQuote(ctx, req.QuoteID)
	if err != nil {
		return nil, err
	}
	if !s.now().Before(quote.ExpiresAt) && quote.AcceptedAt == nil {
		return nil, ErrQuoteExpired
	}
	if quote.IdempotencyKey != nil && strings.TrimSpace(*quote.IdempotencyKey) != "" && strings.TrimSpace(*quote.IdempotencyKey) != key {
		return nil, ErrIdempotencyReuse
	}
	_, order, err := planFromQuote(quote)
	if err != nil {
		return nil, err
	}
	if err := driver.BisqReady(ctx, order); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAutomatedDisabled, err)
	}
	tx, err := store.AcceptBisqQuote(ctx, quote.ID, key, func(q *db.BisqExecutionQuote, now time.Time) (*db.Transaction, error) {
		return acceptedRow(q, key, now)
	})
	if err != nil {
		return nil, err
	}
	if s.enqueue != nil {
		s.enqueue(tx.ID)
	}
	return &AcceptResult{TransactionID: tx.ID, Accepted: true, Stage: db.BisqStageAccepted, TradeMode: quote.TradeMode}, nil
}

func (s *Service) Execute(ctx context.Context, transactionID uint) {
	s.executeDurable(ctx, transactionID)
}

func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.resume(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) resume(ctx context.Context) {
	store, ok := s.store.(DurableStore)
	if !s.enabled || !ok {
		return
	}
	rows, err := store.ListBisqRuns(ctx)
	if err != nil {
		return
	}
	for i := range rows {
		s.Execute(ctx, rows[i].TransactionID)
	}
}

func planFromQuote(quote *db.BisqExecutionQuote) (*db.BisqExecutionPlan, tradeflow.Order, error) {
	var daemonQuote bisq.TakeOfferQuote
	if err := json.Unmarshal(quote.Quote, &daemonQuote); err != nil {
		return nil, tradeflow.Order{}, fmt.Errorf("parse stored daemon quote: %w", err)
	}
	var limits db.BisqSpendingLimits
	if err := json.Unmarshal(quote.Limits, &limits); err != nil {
		return nil, tradeflow.Order{}, fmt.Errorf("parse stored limits: %w", err)
	}
	var route db.ResolvedRoute
	if err := json.Unmarshal(quote.ResolvedRoute, &route); err != nil {
		return nil, tradeflow.Order{}, fmt.Errorf("parse stored resolved_route: %w", err)
	}
	fiat := bisq.FiatAmountFromVolumeAsLong(daemonQuote.VolumeAsLong)
	plan := &db.BisqExecutionPlan{
		Version:             db.BisqExecutionVersion,
		QuoteID:             quote.ID,
		PaymentAccountID:    quote.PaymentAccountID,
		FundingAddress:      quote.FundingAddress,
		TradeAmountSats:     daemonQuote.TradeAmountSats,
		SecurityDepositSats: daemonQuote.SecurityDepositSats,
		TakerFeeSats:        daemonQuote.TakerFeeSats,
		MiningFeeSats:       daemonQuote.MiningFeeSats,
		TotalToPaySats:      daemonQuote.TotalToPaySats,
		Volume:              daemonQuote.Volume,
		VolumeAsLong:        daemonQuote.VolumeAsLong,
		FiatAmount:          fiat,
		MaxWalletDebitSats:  limits.MaxWalletDebitSats,
		MaxCexFiatSpend:     limits.MaxCexFiatSpend,
		MaxFundingFeeSats:   limits.MaxFundingFeeSats,
		MinFiatProceeds:     limits.MinFiatProceeds,
		MinVolumeAsLong:     int64(math.Ceil(limits.MinFiatProceeds * 1e4)),
	}
	if quote.LocalAccountID != nil {
		plan.LocalAccountID = *quote.LocalAccountID
	}
	order := tradeflow.Order{
		Provider:               route.Provider,
		MarketSide:             route.MarketSide,
		BTCAmount:              dexaccount.SatsToBTC(int64(daemonQuote.TradeAmountSats)),
		FiatCurrency:           route.FiatCurrency,
		RequiredCapital:        route.RequiredCapital,
		RequiredBTCLiquidity:   dexaccount.SatsToBTC(int64(daemonQuote.TotalToPaySats)),
		SecurityDepositBTC:     dexaccount.SatsToBTC(int64(daemonQuote.SecurityDepositSats)),
		HedgeVenue:             route.HedgeVenue,
		HedgeSide:              route.HedgeSide,
		HedgeNotionalBTC:       route.HedgeNotional,
		Leverage:               route.Leverage,
		OfferID:                quote.OfferID,
		Dex:                    "bisq",
		FiatAmount:             fiat,
		SelectedOffer:          quote.SelectedOffer,
		PaymentMethod:          quote.PaymentMethod,
		TradeMode:              quote.TradeMode,
		ResolvedRoute:          &route,
		RoutingFeeAllowanceSat: route.RoutingFeeAllowanceSat,
		CEXWithdrawalFeeBTC:    route.CEXWithdrawalFeeBTC,
		SwapSafetyHeadroomBTC:  route.SwapSafetyHeadroomBTC,
		BisqPlan:               plan,
	}
	return plan, order, nil
}

func matchPaymentAccount(accounts []bisq.PaymentAccount, id, method, currency string) (bisq.PaymentAccount, error) {
	id = strings.TrimSpace(id)
	for _, account := range accounts {
		if account.ID != id {
			continue
		}
		if method != "" && account.PaymentMethodID != "" && !strings.EqualFold(account.PaymentMethodID, method) && !strings.Contains(strings.ToLower(method), strings.ToLower(account.PaymentMethodID)) {
			return bisq.PaymentAccount{}, fmt.Errorf("mapped Bisq account %s payment method %s does not match %s", id, account.PaymentMethodID, method)
		}
		if currency != "" && account.TradeCurrency != "" && !strings.EqualFold(account.TradeCurrency, currency) {
			return bisq.PaymentAccount{}, fmt.Errorf("mapped Bisq account %s currency %s does not match %s", id, account.TradeCurrency, currency)
		}
		return account, nil
	}
	return bisq.PaymentAccount{}, fmt.Errorf("mapped Bisq payment account %s is not present on the daemon", id)
}
