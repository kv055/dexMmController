package bisqexec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dexTakerController/internal/db"
	"dexTakerController/internal/tradeflow"
)

type durableFake struct {
	*fakeStore
	mu      sync.Mutex
	runs    map[uint]*db.BisqExecutionRun
	actions map[uint][]db.BisqAction
	leases  map[uint]db.BisqLease
	fence   int64
}

func newDurableFake() *durableFake {
	return &durableFake{fakeStore: newFakeStore(), runs: map[uint]*db.BisqExecutionRun{}, actions: map[uint][]db.BisqAction{}, leases: map[uint]db.BisqLease{}}
}
func (f *durableFake) AcceptBisqQuote(ctx context.Context, id, key string, build func(*db.BisqExecutionQuote, time.Time) (*db.Transaction, error)) (*db.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if r.QuoteID == id || r.RequestKey == key {
			if r.QuoteID != id || r.RequestKey != key {
				return nil, db.ErrBisqConflict
			}
			return f.GetTransaction(r.TransactionID)
		}
	}
	q, err := f.GetBisqExecutionQuote(ctx, id)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(q.ExpiresAt) {
		return nil, db.ErrBisqExpired
	}
	tx, err := build(q, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err = f.CreateTransaction(tx); err != nil {
		return nil, err
	}
	now := time.Now()
	q.TransactionID = &tx.ID
	q.IdempotencyKey = &key
	q.AcceptedAt = &now
	_ = f.UpdateBisqExecutionQuote(ctx, q)
	f.runs[tx.ID] = &db.BisqExecutionRun{TransactionID: tx.ID, QuoteID: id, RequestKey: key, Version: 2, Stage: db.BisqStageAccepted, DeadlineAt: now.Add(30 * time.Minute)}
	return tx, nil
}
func (f *durableFake) ClaimBisqRun(_ context.Context, id uint, owner string) (*db.BisqLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.leases[id]; ok && l.LeaseUntil.After(time.Now()) {
		return nil, db.ErrBisqLeaseLost
	}
	f.fence++
	l := db.BisqLease{TransactionID: id, Owner: owner, Fence: f.fence, LeaseUntil: time.Now().Add(2 * time.Minute)}
	f.leases[id] = l
	return &l, nil
}
func (f *durableFake) RenewBisqLease(_ context.Context, l db.BisqLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leases[l.TransactionID].Owner != l.Owner {
		return db.ErrBisqLeaseLost
	}
	return nil
}
func (f *durableFake) ReleaseBisqLease(_ context.Context, l db.BisqLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leases[l.TransactionID].Owner == l.Owner {
		delete(f.leases, l.TransactionID)
	}
	return nil
}
func (f *durableFake) BeginBisqAction(_ context.Context, l db.BisqLease, a db.BisqAction) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leases[l.TransactionID].Owner != l.Owner {
		return false, db.ErrBisqLeaseLost
	}
	for _, old := range f.actions[l.TransactionID] {
		if old.Kind == a.Kind {
			return false, nil
		}
	}
	a.State = "intent"
	f.actions[l.TransactionID] = append(f.actions[l.TransactionID], a)
	return true, nil
}
func (f *durableFake) CompleteBisqAction(_ context.Context, l db.BisqLease, kind string, raw json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leases[l.TransactionID].Owner != l.Owner {
		return db.ErrBisqLeaseLost
	}
	for i := range f.actions[l.TransactionID] {
		if f.actions[l.TransactionID][i].Kind == kind {
			f.actions[l.TransactionID][i].State = "confirmed"
			f.actions[l.TransactionID][i].Receipt = raw
			return nil
		}
	}
	return errors.New("missing action")
}
func (f *durableFake) GetBisqRun(_ context.Context, id uint) (*db.BisqExecutionRun, []db.BisqAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[id]
	if r == nil {
		return nil, nil, errors.New("legacy row")
	}
	cp := *r
	return &cp, append([]db.BisqAction(nil), f.actions[id]...), nil
}
func (f *durableFake) ListBisqRuns(context.Context) ([]db.BisqExecutionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.BisqExecutionRun
	for _, r := range f.runs {
		out = append(out, *r)
	}
	return out, nil
}
func (f *durableFake) ParkBisqRun(_ context.Context, l db.BisqLease, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[l.TransactionID].Stage = db.BisqStageReviewRequired
	f.runs[l.TransactionID].ReviewReason = reason
	return nil
}
func (f *durableFake) FinishBisqRun(_ context.Context, l db.BisqLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[l.TransactionID].Stage = db.BisqStageTaken
	return nil
}

func (r *recordingRunner) BisqReady(context.Context, tradeflow.Order) error { return nil }
func (r *recordingRunner) NextBisqStep(_ context.Context, o tradeflow.Order, a []db.BisqAction) (*tradeflow.BisqStep, error) {
	if len(a) > 0 && a[0].State == "confirmed" {
		return nil, nil
	}
	return &tradeflow.BisqStep{Kind: "take", Request: json.RawMessage(`{}`), Perform: func(ctx context.Context, key string) (json.RawMessage, error) {
		r.orders = append(r.orders, o)
		return json.RawMessage(`{"trade_id":"t1","fiat_amount":550}`), nil
	}}, nil
}

type crashDriver struct {
	*recordingRunner
	calls     int
	reconcile int
	waiting   bool
}

func (d *crashDriver) NextBisqStep(_ context.Context, o tradeflow.Order, a []db.BisqAction) (*tradeflow.BisqStep, error) {
	if len(a) > 0 && a[0].State == "confirmed" {
		return nil, nil
	}
	return &tradeflow.BisqStep{Kind: "take", Request: json.RawMessage(`{}`), Perform: func(context.Context, string) (json.RawMessage, error) {
		d.calls++
		return nil, errors.New("reply lost after venue accepted")
	}, Reconcile: func(context.Context, db.BisqAction) (json.RawMessage, error) {
		d.reconcile++
		if d.waiting {
			return nil, tradeflow.ErrBisqWaiting
		}
		return json.RawMessage(`{"trade_id":"t1","fiat_amount":550}`), nil
	}}, nil
}

func seededRun(t *testing.T, store *durableFake) uint {
	t.Helper()
	plan := &db.BisqExecutionPlan{Version: 2, DeadlineAt: time.Now().Add(time.Hour)}
	order, _ := json.Marshal(tradeflow.Order{Dex: "bisq", BisqPlan: plan})
	raw, _ := db.MarshalDexOfferCreation(db.DexOfferMeta{BisqExecution: plan, TradeflowOrder: order})
	tx := &db.Transaction{Status: db.StatusCexTradeflowStarted, DexOfferCreation: raw}
	_ = store.CreateTransaction(tx)
	store.runs[tx.ID] = &db.BisqExecutionRun{Version: 2, TransactionID: tx.ID, DeadlineAt: plan.DeadlineAt, Stage: db.BisqStageAccepted}
	return tx.ID
}
func TestLostReplyIsReconciledWithoutRepeatingAction(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	driver := &crashDriver{recordingRunner: &recordingRunner{}, waiting: true}
	svc := New(nil, store, nil, driver).WithEnabled(true)
	svc.Execute(context.Background(), id)
	svc.Execute(context.Background(), id)
	if driver.calls != 1 || driver.reconcile != 1 {
		t.Fatalf("calls=%d reconcile=%d", driver.calls, driver.reconcile)
	}
	driver.waiting = false
	svc.Execute(context.Background(), id)
	if driver.calls != 1 || store.runs[id].Stage != db.BisqStageTaken {
		t.Fatal("did not reconcile recorded action")
	}
}
func TestExpiredRunReconcilesButDoesNotStartNewAction(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	driver := &crashDriver{recordingRunner: &recordingRunner{}}
	svc := New(nil, store, nil, driver).WithEnabled(true)
	svc.Execute(context.Background(), id)
	store.runs[id].DeadlineAt = time.Now().Add(-time.Minute)
	svc.Execute(context.Background(), id)
	if driver.calls != 1 || driver.reconcile != 1 {
		t.Fatal("expired pending intent was repeated or not reconciled")
	}
}
func TestDisabledExecutorDoesNotSpend(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	driver := &recordingRunner{}
	New(nil, store, nil, driver).Execute(context.Background(), id)
	if len(driver.orders) != 0 {
		t.Fatal("disabled executor moved money")
	}
}
func TestSameTransactionCannotClaimTwice(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	l, err := store.ClaimBisqRun(context.Background(), id, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimBisqRun(context.Background(), id, "second"); !errors.Is(err, db.ErrBisqLeaseLost) {
		t.Fatal("concurrent ownership allowed")
	}
	_ = store.ReleaseBisqLease(context.Background(), db.BisqLease{TransactionID: id, Owner: "stale"})
	if err = store.RenewBisqLease(context.Background(), *l); err != nil {
		t.Fatal("stale owner released current claim")
	}
}

func TestExpiredRunWithoutIntentDoesNotPerform(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	store.runs[id].DeadlineAt = time.Now().Add(-time.Second)
	driver := &crashDriver{recordingRunner: &recordingRunner{}}
	New(nil, store, nil, driver).WithEnabled(true).Execute(context.Background(), id)
	if driver.calls != 0 || store.runs[id].Stage != db.BisqStageReviewRequired || len(store.actions[id]) != 0 {
		t.Fatal("expired unspent run was not parked before intent")
	}
}

func TestChangedIntentIsParkedWithoutReconciliation(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	store.actions[id] = []db.BisqAction{{TransactionID: id, Kind: "take", State: "intent", Request: json.RawMessage(`{"amount":1}`)}}
	driver := &crashDriver{recordingRunner: &recordingRunner{}}
	New(nil, store, nil, driver).WithEnabled(true).Execute(context.Background(), id)
	if driver.calls != 0 || driver.reconcile != 0 || store.runs[id].Stage != db.BisqStageReviewRequired {
		t.Fatal("changed action request was used")
	}
}

func TestSameRequestPreservesExactIntegers(t *testing.T) {
	if sameRequest(json.RawMessage(`{"sats":9007199254740992}`), json.RawMessage(`{"sats":9007199254740993}`)) {
		t.Fatal("distinct integers became equal through float rounding")
	}
	if !sameRequest(json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{ "b":2, "a":1 }`)) {
		t.Fatal("JSON key order incorrectly changes immutable request")
	}
}

type blockingDriver struct {
	*recordingRunner
	entered chan struct{}
	finish  chan struct{}
	calls   atomic.Int32
}

func (d *blockingDriver) NextBisqStep(_ context.Context, _ tradeflow.Order, actions []db.BisqAction) (*tradeflow.BisqStep, error) {
	if len(actions) > 0 && actions[0].State == "confirmed" {
		return nil, nil
	}
	return &tradeflow.BisqStep{Kind: "take", Request: json.RawMessage(`{}`), Perform: func(ctx context.Context, _ string) (json.RawMessage, error) {
		if d.calls.Add(1) == 1 {
			close(d.entered)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.finish:
			return json.RawMessage(`{"trade_id":"once","fiat_amount":550}`), nil
		}
	}}, nil
}

func TestConcurrentExecutorsDoNotShareAnActiveLease(t *testing.T) {
	store := newDurableFake()
	id := seededRun(t, store)
	driver := &blockingDriver{recordingRunner: &recordingRunner{}, entered: make(chan struct{}), finish: make(chan struct{})}
	svc := New(nil, store, nil, driver).WithEnabled(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { svc.Execute(ctx, id); close(done) }()
	select {
	case <-driver.entered:
	case <-ctx.Done():
		t.Fatal("first worker did not enter action")
	}
	svc.Execute(ctx, id)
	close(driver.finish)
	<-done
	if driver.calls.Load() != 1 {
		t.Fatal("concurrent worker submitted the same financial action")
	}
}
