package tradeflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"dexTakerController/internal/db"
	"dexTakerController/internal/dexaccount"
	"dexTakerController/internal/exchangeconnectors/dex/robosats"
)

func TestRoboSatsDoesNotRetryAnAmbiguousInitialHedge(t *testing.T) {
	svc := happyFake()
	svc.hedgeErr = errors.New("lost hedge response")
	order := Order{Dex: "robosats", QuoteID: "q", HedgeVenue: "binance_futures", HedgeSide: "short", HedgeNotionalBTC: 0.01}
	if _, err := New(svc, newFakeStore()).openHedgeWithRetry(context.Background(), order); err == nil {
		t.Fatal("ambiguous hedge accepted")
	}
	if svc.hedgeCallCount != 1 {
		t.Fatal("hedge was retried")
	}
	if routingFeeLimit(order) != 0 {
		t.Fatal("zero quoted routing budget was increased")
	}
}

type roboMemory struct {
	*fakeStore
	job     db.RoboSatsJob
	session *robosats.Session
	quote   *db.ExecutionQuote
}

func (r *roboMemory) AdmitRoboSats(context.Context, string, json.RawMessage, *robosats.Session) (uint, error) {
	return 0, fmt.Errorf("unexpected admission")
}
func (r *roboMemory) LoadExecutionQuote(context.Context, string) (*db.ExecutionQuote, error) {
	return r.quote, nil
}
func (r *roboMemory) RoboSatsJobs(context.Context) ([]db.RoboSatsJob, error) {
	return []db.RoboSatsJob{r.job}, nil
}
func (r *roboMemory) SetRoboSatsJob(_ context.Context, _ uint, state, reason string) error {
	r.job.State, r.job.LastError = state, reason
	return nil
}
func (r *roboMemory) LockRoboSatsExecution(context.Context, uint) (func(), error) {
	return func() {}, nil
}
func (r *roboMemory) LockRoboSats(context.Context, uint64) (func(), error) { return func() {}, nil }
func (r *roboMemory) LoadRoboSatsSession(context.Context, uint64) (*robosats.Session, error) {
	return r.session, nil
}
func (r *roboMemory) CreateRoboSatsSession(context.Context, *robosats.Session) (*robosats.Session, error) {
	return nil, fmt.Errorf("unexpected session creation")
}
func (r *roboMemory) SaveRoboSatsSession(_ context.Context, s *robosats.Session) error {
	r.session = s
	return nil
}

func TestBoundAdmissionDoesNotRepeatPreflightOrMoneyMovement(t *testing.T) {
	r := &roboMemory{fakeStore: newFakeStore(), quote: &db.ExecutionQuote{TransactionID: 42}}
	svc := happyFake()
	id, err := New(svc, r).AdmitRoboSats(context.Background(), Order{QuoteID: "already-bound"})
	if err != nil || id != 42 || svc.spotCallCount != 0 || svc.preTradeCallCount != 0 {
		t.Fatalf("retry id=%d err=%v calls=%d", id, err, svc.spotCallCount)
	}
}

func TestRoboSatsWorkerDoesNotReplayAmbiguousCEXOperations(t *testing.T) {
	for _, status := range []db.TransactionStatus{db.StatusCexTradeflowStarted, db.StatusCexTradeSuccessful, db.StatusHedgeFailedNeedsReview} {
		t.Run(string(status), func(t *testing.T) {
			r := &roboMemory{fakeStore: newFakeStore(), job: db.RoboSatsJob{TransactionID: 1, State: "running", OrderData: json.RawMessage(`{"Dex":"robosats","QuoteID":"q"}`)}}
			r.CreateTransaction(&db.Transaction{Status: status})
			svc := happyFake()
			tf := New(svc, r)
			// Deliberately stale queued snapshot: the locked reload must win.
			if err := tf.runRoboSatsJob(context.Background(), r, db.RoboSatsJob{TransactionID: 1, State: "queued"}); err != nil {
				t.Fatal(err)
			}
			if r.job.State != "needs_review" || svc.spotCallCount+svc.hedgeCallCount+svc.withdrawCallCount != 0 {
				t.Fatal("ambiguous CEX operation replayed")
			}
		})
	}
}

func TestRoboSatsHedgelessResumeRefillsOnceFromDurableEscrow(t *testing.T) {
	order := hedgelessOrder("robosats")
	order.QuoteID = "q"
	order.SelectedOffer = json.RawMessage(`{"exchange":"robosats","offer_id":"offer-1","venue_coordinator_id":"lake","quote_id":"q"}`)
	r := &roboMemory{fakeStore: newFakeStore()}
	id, err := createHedgelessRow(r, order, 0.1, "bc1qlnd")
	if err != nil {
		t.Fatal(err)
	}
	r.session = &robosats.Session{Funded: true, PreparationComplete: true, Phase: robosats.PhaseEscrowLocked, Request: robosats.TakeRequest{RequestID: uint64(id), BondFeeSat: 5, EscrowFeeSat: 5}, Payments: map[string]*robosats.Payment{"escrow": {AmountSat: 1800000, Hash: "escrow"}}}
	raw, _ := json.Marshal(order)
	r.job = db.RoboSatsJob{TransactionID: id, State: "running", OrderData: raw}
	svc := hedgelessService()
	svc.balances = []float64{0.1} // deliberately lagging LND balance
	dex := &fakeHedgelessDEX{result: &dexaccount.TakeResult{Execution: map[string]any{"robosats_funded": true, "exchange": "robosats"}}}
	tf := New(svc, r).WithHedgelessExecution(dex, &fakeHedgelessWallet{}).WithRoboSats(&robosats.Coordinator{Store: r}, true, nil, nil)
	for i := 0; i < 2; i++ {
		if err := tf.runRoboSatsJob(context.Background(), r, r.job); err != nil {
			t.Fatal(err)
		}
	}
	if svc.refillCalls != 1 || svc.withdrawCallCount != 1 || svc.spotCallCount != 0 || svc.hedgeCallCount != 0 || dex.takeCalls != 1 {
		t.Fatalf("replayed funding/refill: %+v takes=%d", svc, dex.takeCalls)
	}
	if svc.refillAmounts[0] < 0.018 {
		t.Fatal("lagging balance suppressed committed escrow refill")
	}
	tx, _ := r.GetTransaction(id)
	if tx.Status != db.StatusDexPositionTaken {
		t.Fatalf("funded trade regressed: %s", tx.Status)
	}
}
