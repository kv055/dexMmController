package tradeflow

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/pretrade"
	lnd "dexMmController/internal/wallethub"
)

type hedgelessFakeService struct {
	*fakeService
	balances       []float64
	refillCalls    int
	refillAmounts  []float64
	withdrawStatus *cexaccount.WithdrawalStatus
}

func (s *hedgelessFakeService) GetWithdrawalStatus(context.Context, string, string, string) (*cexaccount.WithdrawalStatus, error) {
	return s.withdrawStatus, nil
}

type confirmedChain struct{}

func (confirmedChain) TransactionConfirmations(context.Context, string) (int, error) { return 6, nil }

func (s *hedgelessFakeService) LNDPoolBalanceBTC(context.Context, string) (float64, error) {
	if len(s.balances) == 0 {
		return 0, nil
	}
	v := s.balances[0]
	if len(s.balances) > 1 {
		s.balances = s.balances[1:]
	}
	return v, nil
}

func (s *hedgelessFakeService) MarketBuyBTCForRefill(_ context.Context, amount float64, _ pretrade.Params) (*cexaccount.SpotOrderResult, error) {
	s.refillCalls++
	s.refillAmounts = append(s.refillAmounts, amount)
	return &cexaccount.SpotOrderResult{Provider: "binance", OrderID: "refill-1", Status: "filled", Execution: &cexaccount.ExecutionDetail{FilledBTC: amount}}, nil
}

type fakeHedgelessDEX struct {
	takeCalls int
	result    *dexaccount.TakeResult
}

func (d *fakeHedgelessDEX) Take(context.Context, dexaccount.OfferInput) (*dexaccount.TakeResult, error) {
	d.takeCalls++
	return d.result, nil
}
func (d *fakeHedgelessDEX) ResolveDeposit(context.Context, string, map[string]any) (string, string, error) {
	return "", "", nil
}

type fakeHedgelessWallet struct{ sendCalls, payCalls int }

func (w *fakeHedgelessWallet) SendOnChain(context.Context, string, int64, int64) (string, error) {
	w.sendCalls++
	return "dex-funding-tx", nil
}
func (w *fakeHedgelessWallet) PayInvoice(context.Context, string, int64) (string, error) {
	w.payCalls++
	return "payment-hash", nil
}
func (w *fakeHedgelessWallet) DecodeInvoice(context.Context, string) (*lnd.DecodedInvoice, error) {
	return &lnd.DecodedInvoice{Destination: "03destination", PaymentHash: "hash", NumSatoshis: "2000000", Timestamp: strconv.FormatInt(time.Now().Unix(), 10), Expiry: "86400"}, nil
}

type fakeLiquidityExecutor struct {
	err          error
	ensureCalls  int
	releaseCalls int
}

func (f *fakeLiquidityExecutor) EnsureOutbound(context.Context, string, uint, *pretrade.Report) error {
	f.ensureCalls++
	return f.err
}

func (f *fakeLiquidityExecutor) Release(context.Context, string) error {
	f.releaseCalls++
	return nil
}

type fakeBisqResolver struct{ balanceCalls int }

func (r *fakeBisqResolver) DirectDestination(context.Context, string) (string, error) {
	return "bc1qbisq", nil
}
func (r *fakeBisqResolver) DirectBalanceBTC(context.Context, string, string) (float64, error) {
	r.balanceCalls++
	if r.balanceCalls == 1 {
		return 0, nil
	}
	return 0.02, nil
}

func hedgelessOrder(dex string) Order {
	return Order{Provider: "binance", MarketSide: "bid", BTCAmount: 0.018, RequiredBTCLiquidity: 0.02,
		SecurityDepositBTC: 0.002, FiatCurrency: "EUR", RequiredCapital: 1000, OfferID: "offer-1",
		Dex: dex, FiatAmount: 900, PaymentMethod: "SEPA", TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral}
}

func hedgelessService() *hedgelessFakeService {
	base := happyFake()
	base.preTradeReport = &pretrade.Report{OK: true, LNDWithdrawAddress: "bc1qlnd"}
	return &hedgelessFakeService{fakeService: base, balances: []float64{0.10, 0.08}}
}

func TestRunHedgelessHodlHodlFundsBeforeRefillAndSkipsHedge(t *testing.T) {
	svc := hedgelessService()
	svc.balances = []float64{0.10, 0.082}
	store := newFakeStore()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"contract_id": "contract-1", "exchange": "hodlhodl"}, DepositAddress: "bc1qescrow"}}
	wallet := &fakeHedgelessWallet{}
	tf := New(svc, store).WithHedgelessExecution(dex, wallet)

	order := hedgelessOrder("hodlhodl")
	order.SecurityDepositBTC = 0
	order.RequiredBTCLiquidity = order.BTCAmount
	result, err := tf.Run(context.Background(), order)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Hedge != nil || result.DEXFunding == nil || result.PoolRefill == nil {
		t.Fatalf("result = %+v", result)
	}
	if wallet.sendCalls != 1 || wallet.payCalls != 0 || dex.takeCalls != 1 {
		t.Fatalf("calls send/pay/take = %d/%d/%d", wallet.sendCalls, wallet.payCalls, dex.takeCalls)
	}
	if svc.hedgeCallCount != 0 || svc.refillCalls != 1 || svc.withdrawCallCount != 1 {
		t.Fatalf("hedge/refill/withdraw = %d/%d/%d", svc.hedgeCallCount, svc.refillCalls, svc.withdrawCallCount)
	}
	row := store.only(t)
	if row.Status != "DexPositionTaken" || row.BTCPoolRefillStatus != refillWithdrawalInitiated || row.BTCPoolTargetBTC != 0.10 {
		t.Fatalf("row = %+v", row)
	}
}

func TestRunHedgelessCoinbaseRefillGrossesUpAndWithdrawsNet(t *testing.T) {
	svc := hedgelessService()
	svc.balances = []float64{0.10, 0.082}
	order := hedgelessOrder("hodlhodl")
	order.Provider = "coinbase"
	order.CEXWithdrawalFeeBTC = 0.0001
	order.SecurityDepositBTC = 0
	order.RequiredBTCLiquidity = order.BTCAmount
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"contract_id": "contract-1", "exchange": "hodlhodl"}, DepositAddress: "bc1qescrow"}}
	_, err := New(svc, newFakeStore()).WithHedgelessExecution(dex, &fakeHedgelessWallet{}).Run(context.Background(), order)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(svc.refillAmounts) != 1 || math.Abs(svc.refillAmounts[0]-0.0181) > 1e-12 {
		t.Fatalf("refill amounts = %v, want gross 0.01810000", svc.refillAmounts)
	}
	if math.Abs(svc.lastWithdrawAmount-0.018) > 1e-12 {
		t.Fatalf("withdraw amount = %.8f, want net 0.01800000", svc.lastWithdrawAmount)
	}
}

func TestRunHedgelessRoboSatsPaysInvoice(t *testing.T) {
	svc := hedgelessService()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"position_id": "p-1", "exchange": "robosats"}, LNInvoice: "lnbc1invoice"}}
	wallet := &fakeHedgelessWallet{}
	_, err := New(svc, newFakeStore()).WithHedgelessExecution(dex, wallet).Run(context.Background(), hedgelessOrder("robosats"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if wallet.payCalls != 1 || wallet.sendCalls != 0 {
		t.Fatalf("pay/send = %d/%d", wallet.payCalls, wallet.sendCalls)
	}
	if got := svc.refillAmounts[0]; math.Abs(got-0.018) > 1e-12 {
		t.Fatalf("initial refill = %.8f BTC, want traded amount 0.01800000 without the 0.002 bond", got)
	}
}

func TestRunHedgelessBisqFundsIntegratedWalletBeforeTake(t *testing.T) {
	svc := hedgelessService()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"trade_id": "t-1", "exchange": "bisq"}}}
	wallet := &fakeHedgelessWallet{}
	resolver := &fakeBisqResolver{}
	_, err := New(svc, newFakeStore()).WithDirectDestinationResolver(resolver).WithHedgelessExecution(dex, wallet).Run(context.Background(), hedgelessOrder("bisq"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if wallet.sendCalls != 1 || dex.takeCalls != 1 || resolver.balanceCalls < 2 {
		t.Fatalf("send/take/balance = %d/%d/%d", wallet.sendCalls, dex.takeCalls, resolver.balanceCalls)
	}
}

func TestRunHedgelessSafeLiquidityPreparationFailureIsTerminal(t *testing.T) {
	svc := hedgelessService()
	svc.preTradeReport.SwapRequired = true
	svc.preTradeReport.SwapShortfallSat = 50_000
	store := newFakeStore()
	liquidity := &fakeLiquidityExecutor{err: preparationError("balance_check", false, errors.New("wallet unavailable"))}
	tf := New(svc, store).
		WithHedgelessExecution(&fakeHedgelessDEX{}, &fakeHedgelessWallet{}).
		WithLiquidityExecutor(liquidity)

	if _, err := tf.Run(context.Background(), hedgelessOrder("robosats")); err == nil {
		t.Fatal("expected liquidity preparation failure")
	}
	row := store.only(t)
	if row.Status != db.StatusHedgelessPreparationFailed || !row.TransactionComplete {
		t.Fatalf("terminal preparation row = %+v", row)
	}
	if row.BTCPoolRefillStatus != refillRestored {
		t.Fatalf("refill status = %q, want %q", row.BTCPoolRefillStatus, refillRestored)
	}
	if row.StatusReason == nil || !strings.Contains(*row.StatusReason, "wallet unavailable") {
		t.Fatalf("status reason = %v", row.StatusReason)
	}
	if liquidity.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", liquidity.releaseCalls)
	}
}

func TestRunHedgelessRiskyLiquidityPreparationFailureNeedsReview(t *testing.T) {
	svc := hedgelessService()
	svc.preTradeReport.SwapRequired = true
	svc.preTradeReport.SwapShortfallSat = 50_000
	store := newFakeStore()
	liquidity := &fakeLiquidityExecutor{err: preparationError("settlement", true, errors.New("swap settlement unknown"))}
	tf := New(svc, store).
		WithHedgelessExecution(&fakeHedgelessDEX{}, &fakeHedgelessWallet{}).
		WithLiquidityExecutor(liquidity)

	if _, err := tf.Run(context.Background(), hedgelessOrder("robosats")); err == nil {
		t.Fatal("expected ambiguous liquidity preparation failure")
	}
	row := store.only(t)
	if row.Status != db.StatusLiquidityPreparationNeedsReview || row.TransactionComplete {
		t.Fatalf("review preparation row = %+v", row)
	}
	if row.BTCPoolRefillStatus != refillNeedsReview {
		t.Fatalf("refill status = %q, want %q", row.BTCPoolRefillStatus, refillNeedsReview)
	}
	if row.StatusReason == nil || !strings.Contains(*row.StatusReason, "swap settlement unknown") {
		t.Fatalf("status reason = %v", row.StatusReason)
	}
	if liquidity.releaseCalls != 0 {
		t.Fatalf("ambiguous reservation was released %d times", liquidity.releaseCalls)
	}
}

func TestRunHedgelessRejectsSecondActiveCycle(t *testing.T) {
	store := newFakeStore()
	store.rows[7] = &db.Transaction{ID: 7, TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral, BTCPoolRefillStatus: refillNeedsReview}
	_, err := New(hedgelessService(), store).WithHedgelessExecution(&fakeHedgelessDEX{}, &fakeHedgelessWallet{}).Run(context.Background(), hedgelessOrder("hodlhodl"))
	if err == nil {
		t.Fatal("expected active-cycle rejection")
	}
}

func TestAdvanceRefillDoesNotReleaseAmbiguousFundingReservation(t *testing.T) {
	store := newFakeStore()
	store.rows[9] = &db.Transaction{
		ID: 9, TradeMode: cexaccount.TradeModeHedgelessDoubleCollateral,
		Status: db.StatusHedgelessTradeStarted, BTCPoolRefillStatus: refillPending,
		BTCPoolTargetBTC: 0.1, UpdatedAt: time.Now().Add(-10 * time.Minute),
	}
	svc := hedgelessService()
	svc.balances = []float64{0.1}
	tf := New(svc, store)
	if err := tf.advanceRefill(context.Background(), store.rows[9]); err != nil {
		t.Fatalf("advanceRefill: %v", err)
	}
	row := store.rows[9]
	if row.Status != db.StatusLiquidityPreparationNeedsReview || row.BTCPoolRefillStatus != refillNeedsReview {
		t.Fatalf("ambiguous row was not parked: %+v", row)
	}
}

func TestAdvanceRefillWaitsForDepositThenCorrectsReturnFee(t *testing.T) {
	svc := hedgelessService()
	store := newFakeStore()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"trade_id": "trade-1", "exchange": "bisq"}}}
	tf := New(svc, store).WithDirectDestinationResolver(&fakeBisqResolver{}).WithHedgelessExecution(dex, &fakeHedgelessWallet{}).WithChainConfirmationSource(confirmedChain{})
	result, err := tf.Run(context.Background(), hedgelessOrder("bisq"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	row := store.rows[result.TransactionID]
	svc.withdrawStatus = &cexaccount.WithdrawalStatus{CexStatus: "completed", TxHash: "refill-tx"}
	svc.balances = []float64{0.098, 0.098}
	if err := tf.advanceRefill(context.Background(), row); err != nil {
		t.Fatalf("advance initial refill: %v", err)
	}
	if row = store.rows[result.TransactionID]; row.BTCPoolRefillStatus != refillAwaitingDeposit {
		t.Fatalf("pool status = %q, want %q", row.BTCPoolRefillStatus, refillAwaitingDeposit)
	}
	if svc.refillCalls != 1 {
		t.Fatalf("refill calls before deposit return = %d, want 1", svc.refillCalls)
	}

	details := decodeRefillDetails(row.BTCPoolRefillDetails)
	details.SecurityDepositReturn = &securityDepositReturnDetails{Status: "initiated", TxID: "deposit-return-tx"}
	row.BTCPoolRefillDetails, _ = json.Marshal(details)
	store.rows[row.ID] = row
	svc.balances = []float64{0.0999}
	if err := tf.advanceRefill(context.Background(), row); err != nil {
		t.Fatalf("advance returned deposit: %v", err)
	}
	if svc.refillCalls != 2 {
		t.Fatalf("refill calls after confirmed return fee = %d, want 2", svc.refillCalls)
	}
	if got := svc.refillAmounts[1]; math.Abs(got-0.0001) > 1e-12 {
		t.Fatalf("corrective refill = %.8f, want 0.00010000", got)
	}
	row = store.rows[result.TransactionID]
	svc.balances = []float64{0.10}
	if err := tf.advanceRefill(context.Background(), row); err != nil {
		t.Fatalf("complete corrected pool: %v", err)
	}
	row = store.rows[result.TransactionID]
	if row.BTCPoolRefillStatus != refillRestored {
		t.Fatalf("final pool status = %q", row.BTCPoolRefillStatus)
	}
	if audit := decodeRefillDetails(row.BTCPoolRefillDetails).SecurityDepositReturn; audit == nil || audit.Status != "returned" {
		t.Fatalf("final deposit-return audit = %+v", audit)
	}
}

func TestReconcilePoolRefillRequiresRestoredBalance(t *testing.T) {
	svc := hedgelessService()
	store := newFakeStore()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"contract_id": "contract-1", "exchange": "hodlhodl"}, DepositAddress: "bc1qescrow"}}
	tf := New(svc, store).WithHedgelessExecution(dex, &fakeHedgelessWallet{})
	result, err := tf.Run(context.Background(), hedgelessOrder("hodlhodl"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	row := store.rows[result.TransactionID]
	row.BTCPoolRefillStatus = refillNeedsReview
	svc.balances = []float64{0.09}
	if _, err := tf.ReconcilePoolRefill(context.Background(), row.ID); err == nil {
		t.Fatal("expected below-target rejection")
	}
	svc.balances = []float64{0.10}
	reconciled, err := tf.ReconcilePoolRefill(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("ReconcilePoolRefill: %v", err)
	}
	if reconciled.BTCPoolRefillStatus != refillRestored {
		t.Fatalf("status = %q", reconciled.BTCPoolRefillStatus)
	}
}
