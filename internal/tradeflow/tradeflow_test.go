package tradeflow

import (
	"context"
	"errors"
	"testing"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/pretradechecks"
	lnd "dexMmController/internal/wallethub"
)

// fakeService is a controllable Service for unit tests.
type fakeService struct {
	preTradeReport *pretradechecks.Report
	preTradeErr    error
	spotResult     *cexaccount.SpotOrderResult
	spotErr        error
	hedgeResult    *cexaccount.HedgeOpenResult
	hedgeErr       error
	withdrawResult *lnd.WithdrawFromExchangeResult
	withdrawErr    error

	preTradeCallCount int
	spotCallCount     int
	hedgeCallCount    int
	withdrawCallCount int
}

func (f *fakeService) PreTradeCheck(_ context.Context, _, _ string, _ float64) (*pretradechecks.Report, error) {
	f.preTradeCallCount++
	return f.preTradeReport, f.preTradeErr
}

func (f *fakeService) MarketBuyBTC(_ context.Context, _ string, _ float64, _ string, _ float64) (*cexaccount.SpotOrderResult, error) {
	f.spotCallCount++
	return f.spotResult, f.spotErr
}

func (f *fakeService) OpenHedge(_ context.Context, _, _ string, _, _ float64) (*cexaccount.HedgeOpenResult, error) {
	f.hedgeCallCount++
	return f.hedgeResult, f.hedgeErr
}

func (f *fakeService) WithdrawSpotToLND(_ context.Context, _ string, _ float64, _ string) (*lnd.WithdrawFromExchangeResult, error) {
	f.withdrawCallCount++
	return f.withdrawResult, f.withdrawErr
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

func happyFake() *fakeService {
	return &fakeService{
		preTradeReport: &pretradechecks.Report{
			OK:                 true,
			LNDWithdrawAddress: "bc1qtest",
		},
		spotResult: &cexaccount.SpotOrderResult{
			OrderID: "spot-123",
			Status:  "filled",
			Execution: &cexaccount.ExecutionDetail{
				FilledBTC:   0.00999,
				AverageRate: 50000,
			},
		},
		hedgeResult: &cexaccount.HedgeOpenResult{
			Venue:   "binance_futures",
			OrderID: "hedge-456",
			Status:  "filled",
		},
		withdrawResult: &lnd.WithdrawFromExchangeResult{
			WithdrawalID:  "wd-789",
			WalletAddress: "bc1qtest",
		},
	}
}

func TestRun_HappyPath(t *testing.T) {
	svc := happyFake()
	tf := New(svc)

	result, err := tf.Run(context.Background(), validOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpotBuy.OrderID != "spot-123" {
		t.Errorf("spot order_id: want spot-123, got %s", result.SpotBuy.OrderID)
	}
	if result.SpotBuy.FilledBTC != 0.00999 {
		t.Errorf("filled_btc: want 0.00999, got %f", result.SpotBuy.FilledBTC)
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
	svc.preTradeReport = &pretradechecks.Report{
		OK: false,
		Checks: []pretradechecks.CheckResult{
			{Name: "capital", Passed: false, Message: "insufficient balance"},
		},
	}

	_, err := New(svc).Run(context.Background(), validOrder())
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

	_, err := New(svc).Run(context.Background(), validOrder())
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

	_, err := New(svc).Run(context.Background(), validOrder())
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

	_, err := New(svc).Run(context.Background(), validOrder())
	if err == nil {
		t.Fatal("expected error on hedge failure")
	}
	if svc.withdrawCallCount != 0 {
		t.Errorf("withdraw must not be called after hedge failure, called %d times", svc.withdrawCallCount)
	}
}

func TestRun_WithdrawUsesPreTradeAddress(t *testing.T) {
	svc := happyFake()
	var capturedAddr string
	captureService := &captureWithdrawService{fakeService: svc, capturedAddr: &capturedAddr}

	_, err := New(captureService).Run(context.Background(), validOrder())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAddr != "bc1qtest" {
		t.Errorf("withdraw address: want bc1qtest (from pre-trade report), got %q", capturedAddr)
	}
}

func TestRun_PreTradeOKWithoutAddress_SkipsWithdraw(t *testing.T) {
	svc := happyFake()
	svc.preTradeReport = &pretradechecks.Report{OK: true}

	_, err := New(svc).Run(context.Background(), validOrder())
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
	origWithdraw := svc.withdrawResult
	_ = origWithdraw

	// Override withdraw to capture the amount passed.
	captureService := &captureWithdrawService{fakeService: svc, capturedAmount: &capturedAmount}
	result, err := New(captureService).Run(context.Background(), validOrder())
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

func (c *captureWithdrawService) WithdrawSpotToLND(_ context.Context, _ string, amountBTC float64, lndAddr string) (*lnd.WithdrawFromExchangeResult, error) {
	if c.capturedAmount != nil {
		*c.capturedAmount = amountBTC
	}
	if c.capturedAddr != nil {
		*c.capturedAddr = lndAddr
	}
	return c.fakeService.withdrawResult, c.fakeService.withdrawErr
}

func TestRun_NonBidSideRejected(t *testing.T) {
	o := validOrder()
	o.MarketSide = "ask"
	_, err := New(&fakeService{}).Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected error for non-bid market side")
	}
}

func TestRun_ValidationRejectsZeroBTC(t *testing.T) {
	o := validOrder()
	o.BTCAmount = 0
	_, err := New(&fakeService{}).Run(context.Background(), o)
	if err == nil {
		t.Fatal("expected validation error for zero BTCAmount")
	}
}
