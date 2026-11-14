package bisqexec

import (
	"commons/bisqroute"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dexTakerController/internal/db"
	"dexTakerController/internal/exchangeconnectors/dex/bisq"
	"dexTakerController/internal/tradeflow"
)

type fakeDaemon struct {
	capable    bool
	quote      *bisq.TakeOfferQuote
	accounts   []bisq.PaymentAccount
	addrErr    error
	quoteCalls int
}

func (d *fakeDaemon) RequireGuardVersion(context.Context) error {
	if !d.capable {
		return errors.New("unsupported guard version")
	}
	return nil
}

type fakeRouteQuoter struct{}

func (fakeRouteQuoter) RequoteBisq(_ context.Context, q bisqroute.Request) (*db.ResolvedRoute, error) {
	r := testRoute()
	r.BTCAmount = float64(q.TradeAmountSats) / 1e8
	r.FiatAmount = float64(q.VolumeAsLong) / 1e4
	r.RequiredBTCLiquidity = float64(q.TotalToPaySats) / 1e8
	r.SecurityDepositBTC = float64(q.SecurityDepositSats) / 1e8
	r.RequiredCapital = 725
	return r, nil
}
func (d *fakeDaemon) QuoteTakeOffer(context.Context, string, bisq.QuoteTakeOfferParams) (*bisq.TakeOfferQuote, error) {
	d.quoteCalls++
	return d.quote, nil
}
func (d *fakeDaemon) GetPaymentAccounts(context.Context) ([]bisq.PaymentAccount, error) {
	return d.accounts, nil
}
func (d *fakeDaemon) RequireFundingAddress(context.Context, string) error { return d.addrErr }

type fakeStore struct {
	mapping *db.BisqPaymentAccountMapping
	dest    *db.BisqFundingDestination
	quotes  map[string]*db.BisqExecutionQuote
	byKey   map[string]*db.BisqExecutionQuote
	txs     map[uint]*db.Transaction
	nextID  uint
	claimed bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{quotes: map[string]*db.BisqExecutionQuote{}, byKey: map[string]*db.BisqExecutionQuote{}, txs: map[uint]*db.Transaction{}}
}

func (s *fakeStore) GetBisqPaymentAccountMapping(context.Context, string, string) (*db.BisqPaymentAccountMapping, error) {
	return s.mapping, nil
}
func (s *fakeStore) ActiveBisqFundingDestination(context.Context) (*db.BisqFundingDestination, error) {
	return s.dest, nil
}
func (s *fakeStore) CreateBisqExecutionQuote(_ context.Context, quote *db.BisqExecutionQuote) error {
	s.quotes[quote.ID] = quote
	return nil
}
func (s *fakeStore) GetBisqExecutionQuote(_ context.Context, id string) (*db.BisqExecutionQuote, error) {
	q := s.quotes[id]
	if q == nil {
		return nil, db.ErrBisqQuoteNotFound
	}
	return q, nil
}
func (s *fakeStore) GetBisqExecutionQuoteByIdempotency(_ context.Context, key string) (*db.BisqExecutionQuote, error) {
	return s.byKey[key], nil
}
func (s *fakeStore) UpdateBisqExecutionQuote(_ context.Context, quote *db.BisqExecutionQuote) error {
	s.quotes[quote.ID] = quote
	if quote.IdempotencyKey != nil {
		s.byKey[*quote.IdempotencyKey] = quote
	}
	return nil
}
func (s *fakeStore) CreateTransaction(tx *db.Transaction) error {
	s.nextID++
	tx.ID = s.nextID
	cp := *tx
	s.txs[tx.ID] = &cp
	return nil
}
func (s *fakeStore) GetTransaction(id uint) (*db.Transaction, error) {
	tx := s.txs[id]
	if tx == nil {
		return nil, errors.New("missing")
	}
	cp := *tx
	return &cp, nil
}
func (s *fakeStore) UpdateTransaction(tx *db.Transaction) error {
	cp := *tx
	s.txs[tx.ID] = &cp
	return nil
}
func (s *fakeStore) ClaimBisqExecution(context.Context, string, uint, time.Duration) (bool, error) {
	if s.claimed {
		return false, nil
	}
	s.claimed = true
	return true, nil
}
func (s *fakeStore) ReleaseBisqExecutionClaim(context.Context, string, uint) error {
	s.claimed = false
	return nil
}
func (s *fakeStore) ListAcceptedBisqExecutions(context.Context) ([]db.Transaction, error) {
	var out []db.Transaction
	for _, tx := range s.txs {
		out = append(out, *tx)
	}
	return out, nil
}

type fakeWhitelist struct{ addrs []string }

func (w fakeWhitelist) WhitelistedBTCAddresses(context.Context, string) ([]string, error) {
	return w.addrs, nil
}

type recordingRunner struct{ orders []tradeflow.Order }

func (r *recordingRunner) Run(_ context.Context, order tradeflow.Order) (*tradeflow.Result, error) {
	r.orders = append(r.orders, order)
	return &tradeflow.Result{TradeMode: order.TradeMode, TransactionID: order.TransactionID}, nil
}

func testRoute() *db.ResolvedRoute {
	return &db.ResolvedRoute{
		ConfigVersion: 1, SnapshotRevision: 2, ResolvedAt: "2026-08-13T12:00:00Z",
		TradeMode: "hedgefull_single_collateral", TargetDirection: "bid", MarketSide: "bid",
		Provider: "binance", Settlement: "onchain_direct", FiatCurrency: "EUR", FiatAmount: 500,
		BTCAmount: 0.01, RequiredCapital: 1000, HedgeVenue: "binance_futures", HedgeSide: "short",
		HedgeNotional: 0.01, Leverage: 10, CostInputs: db.RouteCostInputs{ReferencePrice: 50000, FeeTier: "economic"},
	}
}

func TestQuoteRejectsMissingMapping(t *testing.T) {
	svc := New(&fakeDaemon{capable: true}, newFakeStore(), fakeWhitelist{}, &recordingRunner{})
	_, err := svc.Quote(context.Background(), QuoteRequest{OfferID: "o1", BTCAmountSats: 1_000_000, TradeMode: "hedgefull_single_collateral", PaymentMethod: "SEPA", FiatCurrency: "EUR", Provider: "binance", ResolvedRoute: testRoute()})
	if err == nil || !errors.Is(err, ErrAutomatedDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestQuoteAndAcceptUseDaemonAmounts(t *testing.T) {
	store := newDurableFake()
	store.mapping = &db.BisqPaymentAccountMapping{LocalAccountID: 7, BisqPaymentAccountID: "acct-9", PaymentMethodName: "SEPA", Currency: "EUR"}
	store.dest = &db.BisqFundingDestination{Address: "bc1qdesignated", IsActive: true}
	daemon := &fakeDaemon{
		capable:  true,
		accounts: []bisq.PaymentAccount{{ID: "acct-9", PaymentMethodID: "SEPA", TradeCurrency: "EUR"}},
		quote: &bisq.TakeOfferQuote{
			OfferID: "offer-1", PaymentAccountID: "acct-9", CurrencyCode: "EUR", IsBuyOffer: true, IsCurrencyForTakerFeeBtc: true,
			TradeAmountSats: 1_000_000, SecurityDepositSats: 150_000, TakerFeeSats: 8_000,
			MiningFeeSats: 2_000, TotalToPaySats: 1_160_000, VolumeAsLong: 5_500_000, Volume: "550.00 EUR",
		},
	}
	runner := &recordingRunner{}
	svc := New(daemon, store, fakeWhitelist{addrs: []string{"bc1qdesignated"}}, runner).WithRouteQuoter(fakeRouteQuoter{}).WithEnabled(true).WithEnqueue(func(uint) {})
	quoted, err := svc.Quote(context.Background(), QuoteRequest{
		OfferID: "offer-1", BTCAmountSats: 1_000_000, TradeMode: "hedgefull_single_collateral",
		PaymentMethod: "SEPA", FiatCurrency: "EUR", Provider: "binance", ResolvedRoute: testRoute(),
	})
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if quoted.TotalToPaySats != 1_160_000 || quoted.FiatAmount != 550 {
		t.Fatalf("quote = %+v", quoted)
	}
	if quoted.Limits.MaxCexFiatSpend != 725 {
		t.Fatalf("client capital was trusted: %+v", quoted.Limits)
	}
	if daemon.quoteCalls != 1 {
		t.Fatalf("quote RPCs=%d; a capability probe must not consume the real quote's rate-limit slot", daemon.quoteCalls)
	}
	accepted, err := svc.Accept(context.Background(), AcceptRequest{QuoteID: quoted.QuoteID, IdempotencyKey: "idem-1"})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	again, err := svc.Accept(context.Background(), AcceptRequest{QuoteID: quoted.QuoteID, IdempotencyKey: "idem-1"})
	if err != nil || again.TransactionID != accepted.TransactionID {
		t.Fatalf("idempotent accept: %+v %v", again, err)
	}
	if _, err := svc.Accept(context.Background(), AcceptRequest{QuoteID: quoted.QuoteID, IdempotencyKey: "idem-2"}); err == nil {
		t.Fatal("expected idempotency reuse error for a second key")
	}
	svc.Execute(context.Background(), accepted.TransactionID)
	if len(runner.orders) != 1 {
		t.Fatalf("run count = %d", len(runner.orders))
	}
	order := runner.orders[0]
	if order.BisqPlan == nil || order.BisqPlan.TradeAmountSats != 1_000_000 {
		t.Fatalf("plan = %+v", order.BisqPlan)
	}
	if order.BTCAmount != 0.01 {
		t.Fatalf("take amount = %v", order.BTCAmount)
	}
	if order.RequiredBTCLiquidity < 0.0116 {
		t.Fatalf("required liquidity = %v, want funded total", order.RequiredBTCLiquidity)
	}
	svc.WithEnabled(false)
	recovered, err := svc.Accept(context.Background(), AcceptRequest{QuoteID: quoted.QuoteID, IdempotencyKey: "idem-1"})
	if err != nil || recovered.TransactionID != accepted.TransactionID || recovered.Stage != db.BisqStageTaken {
		t.Fatalf("disabled read recovery: %+v %v", recovered, err)
	}
}

func TestExecuteSkipsWhenReceiptExists(t *testing.T) {
	store := newFakeStore()
	runner := &recordingRunner{}
	svc := New(&fakeDaemon{capable: true}, store, fakeWhitelist{}, runner)
	tx := &db.Transaction{Status: db.StatusCexTradeflowStarted, CexBitcoinAmount: 0.01}
	_ = store.CreateTransaction(tx)
	plan := &db.BisqExecutionPlan{Stage: db.BisqStageAccepted, DeadlineAt: time.Now().Add(time.Hour)}
	orderJSON, _ := json.Marshal(tradeflow.Order{Dex: "bisq"})
	raw, _ := db.MarshalDexOfferCreation(db.DexOfferMeta{BisqExecution: plan, TradeflowOrder: orderJSON})
	tx.DexOfferCreation = raw
	_ = store.UpdateTransaction(tx)
	svc.Execute(context.Background(), tx.ID)
	if len(runner.orders) != 0 {
		t.Fatal("must not re-run after a financial receipt exists")
	}
}

func TestExecuteDoesNotReplayLegacyExpiredQuote(t *testing.T) {
	store := newFakeStore()
	runner := &recordingRunner{}
	svc := New(&fakeDaemon{capable: true}, store, fakeWhitelist{}, runner)
	svc.now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
	tx := &db.Transaction{Status: db.StatusCexTradeflowStarted}
	_ = store.CreateTransaction(tx)
	plan := &db.BisqExecutionPlan{Stage: db.BisqStageAccepted, DeadlineAt: svc.now().Add(-time.Minute)}
	orderJSON, _ := json.Marshal(tradeflow.Order{Dex: "bisq"})
	raw, _ := db.MarshalDexOfferCreation(db.DexOfferMeta{BisqExecution: plan, TradeflowOrder: orderJSON})
	tx.DexOfferCreation = raw
	_ = store.UpdateTransaction(tx)
	svc.Execute(context.Background(), tx.ID)
	if len(runner.orders) != 0 {
		t.Fatal("expired accepted quotes must not spend")
	}
	got := store.txs[tx.ID]
	if got.Status != db.StatusCexTradeflowStarted {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestQuoteRejectsUnapprovedDestination(t *testing.T) {
	store := newFakeStore()
	store.mapping = &db.BisqPaymentAccountMapping{LocalAccountID: 7, BisqPaymentAccountID: "acct-9", PaymentMethodName: "SEPA", Currency: "EUR"}
	store.dest = &db.BisqFundingDestination{Address: "bc1qother", IsActive: true}
	daemon := &fakeDaemon{capable: true, accounts: []bisq.PaymentAccount{{ID: "acct-9", PaymentMethodID: "SEPA", TradeCurrency: "EUR"}}}
	svc := New(daemon, store, fakeWhitelist{addrs: []string{"bc1qdesignated"}}, &recordingRunner{})
	_, err := svc.Quote(context.Background(), QuoteRequest{
		OfferID: "offer-1", BTCAmountSats: 1_000_000, TradeMode: "hedgefull_single_collateral",
		PaymentMethod: "SEPA", FiatCurrency: "EUR", Provider: "binance", ResolvedRoute: testRoute(),
	})
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("err = %v", err)
	}
}
