package tradeflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

// fakeService is a controllable Service for unit tests.
type fakeService struct {
	preTradeReport *cexaccount.Report
	preTradeErr    error
	spotResult     *cexaccount.SpotOrderResult
	spotErr        error
	hedgeResult    *cexaccount.HedgeOpenResult
	hedgeErr       error
	// hedgeFailFirstN fails the first N OpenHedge attempts, then succeeds.
	hedgeFailFirstN int
	withdrawResult  *lnd.WithdrawFromExchangeResult
	withdrawErr     error
	// livePositions is returned by successive LiveHedgePosition calls (the last
	// element repeats once exhausted); nil yields a flat snapshot.
	livePositions []*cexaccount.HedgeSnapshot
	liveErr       error
	// withdrawAlreadySent is returned by WithdrawalAlreadySent (nil = safe to send).
	withdrawAlreadySent    *cexaccount.WithdrawalStatus
	withdrawAlreadySentErr error

	preTradeCallCount       int
	spotCallCount           int
	hedgeCallCount          int
	withdrawCallCount       int
	liveCallCount           int
	alreadySentCallCount    int
	lastWithdrawClientReqID string
}

func (f *fakeService) PreTradeCheck(_ context.Context, _, _ string, _ float64) (*cexaccount.Report, error) {
	f.preTradeCallCount++
	return f.preTradeReport, f.preTradeErr
}

func (f *fakeService) MarketBuyBTC(_ context.Context, _ string, _ float64, _ string, _ float64) (*cexaccount.SpotOrderResult, error) {
	f.spotCallCount++
	return f.spotResult, f.spotErr
}

func (f *fakeService) OpenHedge(_ context.Context, _, _ string, _, _ float64) (*cexaccount.HedgeOpenResult, error) {
	f.hedgeCallCount++
	if f.hedgeFailFirstN > 0 && f.hedgeCallCount <= f.hedgeFailFirstN {
		return nil, errors.New("hedge transient error")
	}
	if f.hedgeErr != nil {
		return nil, f.hedgeErr
	}
	return f.hedgeResult, nil
}

func (f *fakeService) LiveHedgePosition(_ context.Context, venue string) (*cexaccount.HedgeSnapshot, error) {
	idx := f.liveCallCount
	f.liveCallCount++
	if f.liveErr != nil {
		return nil, f.liveErr
	}
	if idx < len(f.livePositions) {
		return f.livePositions[idx], nil
	}
	if len(f.livePositions) > 0 {
		return f.livePositions[len(f.livePositions)-1], nil
	}
	return &cexaccount.HedgeSnapshot{Venue: venue, Side: "flat"}, nil
}

func (f *fakeService) WithdrawSpotToLND(_ context.Context, _ string, _ float64, _, clientRequestID string) (*lnd.WithdrawFromExchangeResult, error) {
	f.withdrawCallCount++
	f.lastWithdrawClientReqID = clientRequestID
	return f.withdrawResult, f.withdrawErr
}

func (f *fakeService) WithdrawalAlreadySent(_ context.Context, _, _ string, _ float64, _ string, _ time.Duration) (*cexaccount.WithdrawalStatus, error) {
	f.alreadySentCallCount++
	return f.withdrawAlreadySent, f.withdrawAlreadySentErr
}

// fakeStore is an in-memory transactionStore for asserting per-step persistence.
type fakeStore struct {
	rows   map[uint]*db.Transaction
	nextID uint
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[uint]*db.Transaction{}}
}

func (s *fakeStore) CreateTransaction(tx *db.Transaction) error {
	s.nextID++
	tx.ID = s.nextID
	cp := *tx
	s.rows[tx.ID] = &cp
	return nil
}

func (s *fakeStore) GetTransaction(id uint) (*db.Transaction, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, fmt.Errorf("transaction %d not found", id)
	}
	cp := *r
	return &cp, nil
}

func (s *fakeStore) UpdateTransaction(tx *db.Transaction) error {
	if tx.ID == 0 {
		return errors.New("id required")
	}
	cp := *tx
	s.rows[tx.ID] = &cp
	return nil
}

// only returns the single stored row (most persistence tests create exactly one).
func (s *fakeStore) only(t *testing.T) *db.Transaction {
	t.Helper()
	if len(s.rows) != 1 {
		t.Fatalf("expected exactly 1 stored row, got %d", len(s.rows))
	}
	for _, r := range s.rows {
		return r
	}
	return nil
}

func validOrder() Order {
	return Order{
		Provider:           "binance",
		MarketSide:         "bid",
		BTCAmount:          0.01,
		FiatCurrency:       "EUR",
		RequiredCapitalEUR: 1000,
		HedgeVenue:         "binance_futures",
		HedgeSide:          "short",
		HedgeNotionalBTC:   0.01,
		Leverage:           10,
	}
}

// persistOrder links a DEX offer so per-step persistence is enabled.
func persistOrder() Order {
	o := validOrder()
	o.OfferID = "offer-1"
	o.Dex = "hodlhodl"
	o.FiatAmount = 500
	return o
}

func happyFake() *fakeService {
	return &fakeService{
		preTradeReport: &cexaccount.Report{
			OK:                 true,
			LNDWithdrawAddress: "bc1qtest",
		},
		spotResult: &cexaccount.SpotOrderResult{
			OrderID: "spot-123",
			Status:  "filled",
			Execution: &cexaccount.ExecutionDetail{
				FilledBTC:   0.00999,
				AverageRate: 50000,
				Fees:        []cexaccount.FeeLine{{Asset: "EUR", Amount: 0.25}},
			},
		},
		hedgeResult: &cexaccount.HedgeOpenResult{
			Venue:   "binance_futures",
			OrderID: "hedge-456",
			Status:  "filled",
		},
		withdrawResult: &lnd.WithdrawFromExchangeResult{
			WithdrawalID:    "wd-789",
			WalletAddress:   "bc1qtest",
			ClientRequestID: "tradeflow-binance-1",
		},
	}
}

func TestRun_HappyPath(t *testing.T) {
	svc := happyFake()
	tf := New(svc, nil)

	result, err := tf.Run(context.Background(), validOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpotBuy.OrderID != "spot-123" {
		t.Errorf("spot order_id: want spot-123, got %s", result.SpotBuy.OrderID)
	}
	if result.SpotBuy.Execution == nil || result.SpotBuy.Execution.FilledBTC != 0.00999 {
		t.Errorf("filled_btc: want 0.00999, got %+v", result.SpotBuy.Execution)
	}
	if result.Hedge.OrderID != "hedge-456" {
		t.Errorf("hedge order_id: want hedge-456, got %s", result.Hedge.OrderID)
	}
	if result.Withdrawal.WalletAddress != "bc1qtest" {
		t.Errorf("wallet address: want bc1qtest, got %s", result.Withdrawal.WalletAddress)
	}
	if result.Withdrawal.AmountBTC != 0.00999 {
		t.Errorf("withdrawal amount should equal FilledBTC (0.00999), got %f", result.Withdrawal.AmountBTC)
	}
	if result.Withdrawal.Provider != "binance" {
		t.Errorf("withdrawal provider: want binance, got %s", result.Withdrawal.Provider)
	}
	if result.Withdrawal.CexStatus != "submitted" {
		t.Errorf("withdrawal cex_status: want submitted, got %s", result.Withdrawal.CexStatus)
	}
	if result.Withdrawal.ClientRequestID != "tradeflow-binance-1" {
		t.Errorf("client_request_id: want tradeflow-binance-1, got %s", result.Withdrawal.ClientRequestID)
	}

	// All four service calls must have been made exactly once.
	if svc.preTradeCallCount != 1 {
		t.Errorf("pre-trade check called %d times, want 1", svc.preTradeCallCount)
	}
	if svc.spotCallCount != 1 {
		t.Errorf("spot buy called %d times, want 1", svc.spotCallCount)
	}
	if svc.hedgeCallCount != 1 {
		t.Errorf("hedge open called %d times, want 1", svc.hedgeCallCount)
	}
	if svc.withdrawCallCount != 1 {
		t.Errorf("withdraw called %d times, want 1", svc.withdrawCallCount)
	}
}

func TestRun_PreTradeCheckFails_ShortCircuits(t *testing.T) {
	svc := happyFake()
	svc.preTradeReport = &cexaccount.Report{
		OK: false,
		Checks: []cexaccount.CheckResult{
			{Name: "capital", Passed: false, Message: "insufficient balance"},
		},
	}

	_, err := New(svc, nil).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error when pre-trade checks fail")
	}
	if svc.spotCallCount != 0 {
		t.Errorf("spot buy must not be called when pre-trade checks fail, called %d times", svc.spotCallCount)
	}
	if svc.hedgeCallCount != 0 {
		t.Errorf("hedge must not be called when pre-trade checks fail, called %d times", svc.hedgeCallCount)
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("withdraw must not be called when pre-trade checks fail, called %d times", svc.withdrawCallCount)
	}
}

func TestRun_PreTradeCheckError_ShortCircuits(t *testing.T) {
	svc := happyFake()
	svc.preTradeReport = nil
	svc.preTradeErr = errors.New("db unreachable")

	_, err := New(svc, nil).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error on pre-trade check infrastructure failure")
	}
	if svc.spotCallCount != 0 {
		t.Errorf("spot buy must not be called on pre-trade error, called %d times", svc.spotCallCount)
	}
}

func TestRun_SpotBuyFails_SkipsHedgeAndWithdraw(t *testing.T) {
	svc := happyFake()
	svc.spotErr = errors.New("exchange offline")

	_, err := New(svc, nil).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error on spot buy failure")
	}
	if svc.hedgeCallCount != 0 {
		t.Errorf("hedge must not be called after spot failure, called %d times", svc.hedgeCallCount)
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("withdraw must not be called after spot failure, called %d times", svc.withdrawCallCount)
	}
}

func TestRun_HedgeFails_SkipsWithdraw(t *testing.T) {
	svc := happyFake()
	svc.hedgeErr = errors.New("futures API error")

	_, err := New(svc, nil).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error on hedge failure")
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("withdraw must not be called after hedge failure, called %d times", svc.withdrawCallCount)
	}
	if svc.hedgeCallCount != hedgeMaxAttempts {
		t.Errorf("hedge should be retried %d times, got %d", hedgeMaxAttempts, svc.hedgeCallCount)
	}
}

func TestRun_WithdrawUsesPreTradeAddress(t *testing.T) {
	svc := happyFake()
	var capturedAddr string
	captureService := &captureWithdrawService{fakeService: svc, capturedAddr: &capturedAddr}

	_, err := New(captureService, nil).Run(context.Background(), validOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAddr != "bc1qtest" {
		t.Errorf("withdraw address: want bc1qtest (from pre-trade report), got %q", capturedAddr)
	}
}

func TestRun_PreTradeOKWithoutAddress_SkipsWithdraw(t *testing.T) {
	svc := happyFake()
	svc.preTradeReport = &cexaccount.Report{OK: true}

	_, err := New(svc, nil).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error when pre-trade OK but LNDWithdrawAddress is empty")
	}
	if svc.spotCallCount != 0 {
		t.Errorf("spot buy must not run without withdrawal address, called %d times", svc.spotCallCount)
	}
}

func TestRun_WithdrawUsesFilledBTC(t *testing.T) {
	svc := happyFake()
	svc.spotResult.Execution.FilledBTC = 0.00987 // different from BTCAmount

	var capturedAmount float64
	svc.withdrawResult = &lnd.WithdrawFromExchangeResult{WithdrawalID: "x", WalletAddress: "bc1q"}

	// Override withdraw to capture the amount passed.
	captureService := &captureWithdrawService{fakeService: svc, capturedAmount: &capturedAmount}
	result, err := New(captureService, nil).Run(context.Background(), validOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAmount != 0.00987 {
		t.Errorf("withdraw amount: want 0.00987 (FilledBTC), got %f", capturedAmount)
	}
	if result.Withdrawal.AmountBTC != 0.00987 {
		t.Errorf("result.Withdrawal.AmountBTC: want 0.00987, got %f", result.Withdrawal.AmountBTC)
	}
}

// captureWithdrawService wraps fakeService and captures withdraw call arguments.
type captureWithdrawService struct {
	*fakeService
	capturedAmount *float64
	capturedAddr   *string
}

func (c *captureWithdrawService) WithdrawSpotToLND(_ context.Context, _ string, amountBTC float64, lndAddr, _ string) (*lnd.WithdrawFromExchangeResult, error) {
	if c.capturedAmount != nil {
		*c.capturedAmount = amountBTC
	}
	if c.capturedAddr != nil {
		*c.capturedAddr = lndAddr
	}
	return c.fakeService.withdrawResult, c.fakeService.withdrawErr
}

func TestRun_AskSideNotImplemented(t *testing.T) {
	o := validOrder()
	o.MarketSide = "ask"
	_, err := New(&fakeService{}, nil).Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected error for ask market side")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not-implemented error, got: %v", err)
	}
}

func TestRun_UnsupportedMarketSideRejected(t *testing.T) {
	o := validOrder()
	o.MarketSide = "unknown"
	_, err := New(&fakeService{}, nil).Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected error for unsupported market side")
	}
}

func TestRun_ValidationRejectsZeroBTC(t *testing.T) {
	o := validOrder()
	o.BTCAmount = 0
	_, err := New(&fakeService{}, nil).Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected validation error for zero BTCAmount")
	}
}

// --- Per-step persistence -------------------------------------------------

func TestRun_Persist_HappyPath_FinalStatus(t *testing.T) {
	svc := happyFake()
	store := newFakeStore()

	result, err := New(svc, store).Run(context.Background(), persistOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TransactionID == 0 {
		t.Fatal("expected a persisted transaction id on the result")
	}
	row := store.only(t)
	if row.Status != db.StatusCexCryptoWithdrawalInitiated {
		t.Errorf("final status: want %s, got %s", db.StatusCexCryptoWithdrawalInitiated, row.Status)
	}
	if row.CexBitcoinAmount != 0.00999 {
		t.Errorf("CexBitcoinAmount: want 0.00999, got %f", row.CexBitcoinAmount)
	}
	if len(row.CexCounterPositionOpenDetails) == 0 {
		t.Error("expected hedge open details to be persisted")
	}
	// Withdrawal idempotency key must be derived from the transaction id.
	wantKey := fmt.Sprintf("tradeflow-%d", result.TransactionID)
	if svc.lastWithdrawClientReqID != wantKey {
		t.Errorf("withdraw client req id: want %s, got %s", wantKey, svc.lastWithdrawClientReqID)
	}
}

func TestRun_Persist_HedgeExhausted_MarksNeedsReview(t *testing.T) {
	svc := happyFake()
	svc.hedgeErr = errors.New("futures down") // always fails; live stays flat
	store := newFakeStore()

	_, err := New(svc, store).Run(context.Background(), persistOrder())
	if err == nil {
		t.Fatal("expected error when hedge cannot be opened")
	}
	if svc.hedgeCallCount != hedgeMaxAttempts {
		t.Errorf("hedge attempts: want %d, got %d", hedgeMaxAttempts, svc.hedgeCallCount)
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("withdraw must not run after hedge failure, called %d", svc.withdrawCallCount)
	}
	row := store.only(t)
	if row.Status != db.StatusHedgeFailedNeedsReview {
		t.Errorf("status: want %s, got %s", db.StatusHedgeFailedNeedsReview, row.Status)
	}
}

func TestRun_Persist_HedgeRetrySucceeds(t *testing.T) {
	svc := happyFake()
	svc.hedgeFailFirstN = 1 // first attempt fails, second succeeds
	store := newFakeStore()

	_, err := New(svc, store).Run(context.Background(), persistOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.hedgeCallCount != 2 {
		t.Errorf("hedge should succeed on the 2nd attempt, got %d calls", svc.hedgeCallCount)
	}
	// LiveHedgePosition is consulted for the baseline and after the failed attempt.
	if svc.liveCallCount < 2 {
		t.Errorf("expected live position to be consulted >=2 times, got %d", svc.liveCallCount)
	}
	row := store.only(t)
	if row.Status != db.StatusCexCryptoWithdrawalInitiated {
		t.Errorf("final status: want %s, got %s", db.StatusCexCryptoWithdrawalInitiated, row.Status)
	}
}

func TestRun_Persist_HedgeErrorButPositionOpen_NoDoubleOpen(t *testing.T) {
	svc := happyFake()
	svc.hedgeErr = errors.New("lost confirmation") // call errors every time...
	// ...but the venue reports the short actually opened (baseline flat, then open).
	svc.livePositions = []*cexaccount.HedgeSnapshot{
		{Venue: "binance_futures", Side: "flat"},
		{Venue: "binance_futures", Side: "short", SizeBTC: 0.01},
	}
	store := newFakeStore()

	result, err := New(svc, store).Run(context.Background(), persistOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.hedgeCallCount != 1 {
		t.Errorf("must not re-open hedge once the venue confirms it is open; got %d calls", svc.hedgeCallCount)
	}
	if result.Hedge.Status != "open_detected_after_error" {
		t.Errorf("hedge status: want open_detected_after_error, got %s", result.Hedge.Status)
	}
	row := store.only(t)
	if row.Status != db.StatusCexCryptoWithdrawalInitiated {
		t.Errorf("final status: want %s, got %s", db.StatusCexCryptoWithdrawalInitiated, row.Status)
	}
}

func TestRun_Persist_WithdrawalFails_StaysAtTradeSuccessful(t *testing.T) {
	svc := happyFake()
	svc.withdrawErr = errors.New("withdrawal endpoint 500")
	store := newFakeStore()

	_, err := New(svc, store).Run(context.Background(), persistOrder())
	if err == nil {
		t.Fatal("expected error when withdrawal fails")
	}
	row := store.only(t)
	if row.Status != db.StatusCexTradeSuccessful {
		t.Errorf("status after withdrawal failure: want %s (hedge done, retryable), got %s", db.StatusCexTradeSuccessful, row.Status)
	}
}

func TestRecordSpotBuy_PersistsCexTransactionFee(t *testing.T) {
	svc := happyFake()
	store := newFakeStore()
	order := persistOrder() // FiatCurrency "EUR"
	id, err := createTradeflowRow(store, order, "bc1qtest")
	if err != nil {
		t.Fatalf("createTradeflowRow: %v", err)
	}
	if err := recordSpotBuy(store, id, svc.spotResult, 0.00999); err != nil {
		t.Fatalf("recordSpotBuy: %v", err)
	}
	row, err := store.GetTransaction(id)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	// Fixture spot fill has a 0.25 EUR fee line; it should land in the dedicated column.
	if row.CexTransactionFee != 0.25 {
		t.Errorf("cex_transaction_fee: want 0.25 (EUR fee line), got %v", row.CexTransactionFee)
	}
}

// --- Recovery -------------------------------------------------------------

// tradeSuccessfulRow builds a persisted row at CexTradeSuccessful (spot + hedge
// recorded, withdrawal not yet sent) using the real persist helpers.
func tradeSuccessfulRow(t *testing.T, store *fakeStore, svc *fakeService) *db.Transaction {
	t.Helper()
	order := persistOrder()
	id, err := createTradeflowRow(store, order, "bc1qtest")
	if err != nil {
		t.Fatalf("createTradeflowRow: %v", err)
	}
	if err := recordSpotBuy(store, id, svc.spotResult, 0.00999); err != nil {
		t.Fatalf("recordSpotBuy: %v", err)
	}
	if err := recordHedge(store, id, order, svc.spotResult, svc.hedgeResult); err != nil {
		t.Fatalf("recordHedge: %v", err)
	}
	tx, err := store.GetTransaction(id)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	return tx
}

func TestResumeWithdrawal_IssuesOnceAndAdvances(t *testing.T) {
	svc := happyFake()
	store := newFakeStore()
	tx := tradeSuccessfulRow(t, store, svc)

	if err := New(svc, store).ResumeWithdrawal(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.alreadySentCallCount != 1 {
		t.Errorf("history-match guard should be consulted once, got %d", svc.alreadySentCallCount)
	}
	if svc.withdrawCallCount != 1 {
		t.Errorf("withdrawal should be issued once, got %d", svc.withdrawCallCount)
	}
	wantKey := fmt.Sprintf("tradeflow-%d", tx.ID)
	if svc.lastWithdrawClientReqID != wantKey {
		t.Errorf("client req id: want %s, got %s", wantKey, svc.lastWithdrawClientReqID)
	}
	if row := store.only(t); row.Status != db.StatusCexCryptoWithdrawalInitiated {
		t.Errorf("status: want %s, got %s", db.StatusCexCryptoWithdrawalInitiated, row.Status)
	}
}

func TestResumeWithdrawal_HistoryMatch_NoResend(t *testing.T) {
	svc := happyFake()
	svc.withdrawAlreadySent = &cexaccount.WithdrawalStatus{WithdrawalID: "existing-1", CexStatus: "processing"}
	store := newFakeStore()
	tx := tradeSuccessfulRow(t, store, svc)

	if err := New(svc, store).ResumeWithdrawal(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("must NOT resend when a matching withdrawal already exists, got %d", svc.withdrawCallCount)
	}
	if row := store.only(t); row.Status != db.StatusCexCryptoWithdrawalInitiated {
		t.Errorf("status: want %s, got %s", db.StatusCexCryptoWithdrawalInitiated, row.Status)
	}
}

func TestResumeFromTradeflowStarted_SpotKnown_ResumesHedge(t *testing.T) {
	svc := happyFake()
	store := newFakeStore()
	order := persistOrder()
	id, _ := createTradeflowRow(store, order, "bc1qtest")
	if err := recordSpotBuy(store, id, svc.spotResult, 0.00999); err != nil {
		t.Fatalf("recordSpotBuy: %v", err)
	}
	tx, _ := store.GetTransaction(id)

	if err := New(svc, store).ResumeFromTradeflowStarted(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.spotCallCount != 0 {
		t.Errorf("must NOT re-buy spot during recovery, got %d", svc.spotCallCount)
	}
	if svc.hedgeCallCount < 1 {
		t.Errorf("hedge should be opened, got %d calls", svc.hedgeCallCount)
	}
	if row := store.only(t); row.Status != db.StatusCexTradeSuccessful {
		t.Errorf("status: want %s, got %s", db.StatusCexTradeSuccessful, row.Status)
	}
}

func TestResumeFromTradeflowStarted_SpotUnknown_ParksForReview(t *testing.T) {
	svc := happyFake()
	store := newFakeStore()
	order := persistOrder()
	id, _ := createTradeflowRow(store, order, "bc1qtest") // no spot recorded
	tx, _ := store.GetTransaction(id)

	if err := New(svc, store).ResumeFromTradeflowStarted(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.spotCallCount != 0 || svc.hedgeCallCount != 0 {
		t.Errorf("must not buy or hedge when spot outcome unknown (spot=%d hedge=%d)", svc.spotCallCount, svc.hedgeCallCount)
	}
	if row := store.only(t); row.Status != db.StatusCexTradeflowNeedsReview {
		t.Errorf("status: want %s, got %s", db.StatusCexTradeflowNeedsReview, row.Status)
	}
}

func hedgeFailedRow(t *testing.T, store *fakeStore, svc *fakeService) *db.Transaction {
	t.Helper()
	order := persistOrder()
	id, _ := createTradeflowRow(store, order, "bc1qtest")
	if err := recordSpotBuy(store, id, svc.spotResult, 0.00999); err != nil {
		t.Fatalf("recordSpotBuy: %v", err)
	}
	if err := markHedgeFailed(store, id, svc.spotResult); err != nil {
		t.Fatalf("markHedgeFailed: %v", err)
	}
	tx, _ := store.GetTransaction(id)
	return tx
}

func TestRecoverHedge_VenueShowsOpen_Advances(t *testing.T) {
	svc := happyFake()
	svc.livePositions = []*cexaccount.HedgeSnapshot{{Venue: "binance_futures", Side: "short", SizeBTC: 0.01}}
	store := newFakeStore()
	tx := hedgeFailedRow(t, store, svc)

	if err := New(svc, store).RecoverHedge(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row := store.only(t); row.Status != db.StatusCexTradeSuccessful {
		t.Errorf("status: want %s, got %s", db.StatusCexTradeSuccessful, row.Status)
	}
}

func TestRecoverHedge_StillFlat_StaysForReview(t *testing.T) {
	svc := happyFake() // LiveHedgePosition defaults to flat
	store := newFakeStore()
	tx := hedgeFailedRow(t, store, svc)

	if err := New(svc, store).RecoverHedge(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row := store.only(t); row.Status != db.StatusHedgeFailedNeedsReview {
		t.Errorf("status: want %s (unchanged), got %s", db.StatusHedgeFailedNeedsReview, row.Status)
	}
}
