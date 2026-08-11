package tradeflow

import (
	"context"
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
	balances    []float64
	refillCalls int
}

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
	return &lnd.DecodedInvoice{Destination: "03destination", PaymentHash: "hash", NumSatoshis: "2000000", Timestamp: "1786460000", Expiry: "86400"}, nil
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
	store := newFakeStore()
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"contract_id": "contract-1", "exchange": "hodlhodl"}, DepositAddress: "bc1qescrow"}}
	wallet := &fakeHedgelessWallet{}
	tf := New(svc, store).WithHedgelessExecution(dex, wallet)

	result, err := tf.Run(context.Background(), hedgelessOrder("hodlhodl"))
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
	if row.Status != "DexPositionOpen" || row.BTCPoolRefillStatus != refillWithdrawalInitiated || row.BTCPoolTargetBTC != 0.10 {
		t.Fatalf("row = %+v", row)
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
	if row.Status != db.StatusHedgelessFundingNeedsReview || row.BTCPoolRefillStatus != refillNeedsReview {
		t.Fatalf("ambiguous row was not parked: %+v", row)
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
