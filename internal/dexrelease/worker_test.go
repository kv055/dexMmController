package dexrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"dexTakerController/internal/db"
	"dexTakerController/internal/dexaccount"
)

// fakeStore is an in-memory txStore safe for concurrent use by the
// worker goroutine and test assertions.
type fakeStore struct {
	mu      sync.Mutex
	tx      *db.Transaction
	updates int

	// updated receives a signal on every UpdateTransaction. It is buffered
	// so UpdateTransaction never blocks; tests use it to wait for progress
	// instead of polling unsynchronized fields.
	updated chan struct{}
}

func newFakeStore(tx *db.Transaction) *fakeStore {
	return &fakeStore{tx: tx, updated: make(chan struct{}, 16)}
}

func (f *fakeStore) GetTransaction(id uint) (*db.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tx == nil || f.tx.ID != id {
		return nil, fmt.Errorf("transaction not found")
	}
	cp := *f.tx
	return &cp, nil
}

func (f *fakeStore) UpdateTransaction(tx *db.Transaction) error {
	f.mu.Lock()
	f.updates++
	cp := *tx
	f.tx = &cp
	f.mu.Unlock()
	select {
	case f.updated <- struct{}{}:
	default:
	}
	return nil
}

// snapshot returns a copy of the stored transaction under lock.
func (f *fakeStore) snapshot() *db.Transaction {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tx == nil {
		return nil
	}
	cp := *f.tx
	return &cp
}

// updateCount returns the number of recorded updates under lock.
func (f *fakeStore) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updates
}

// fakeReleaser records calls and returns a canned result/error.
type fakeReleaser struct {
	calls        int
	last         dexaccount.ReleaseInput
	result       *dexaccount.ReleaseResult
	err          error
	returnCalls  int
	lastReturn   dexaccount.SecurityDepositReturnInput
	returnResult *dexaccount.SecurityDepositReturnResult
	returnErr    error
}

func (f *fakeReleaser) ReturnSecurityDeposit(_ context.Context, in dexaccount.SecurityDepositReturnInput) (*dexaccount.SecurityDepositReturnResult, error) {
	f.returnCalls++
	f.lastReturn = in
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.returnResult, nil
}

func (f *fakeReleaser) Release(_ context.Context, in dexaccount.ReleaseInput) (*dexaccount.ReleaseResult, error) {
	f.calls++
	f.last = in
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func fiatArrivedTx() *db.Transaction {
	meta := db.DexOfferMeta{Execution: map[string]any{"exchange": "hodlhodl", "contract_id": "C123"}}
	raw, _ := db.MarshalDexOfferCreation(meta)
	return &db.Transaction{
		ID:               42,
		Dex:              db.DexHodlHodl,
		Status:           db.StatusDexFiatHasArrived,
		DexOfferCreation: raw,
	}
}

func hedgelessBisqFiatArrivedTx() *db.Transaction {
	meta := db.DexOfferMeta{Execution: map[string]any{"exchange": "bisq", "trade_id": "B123"}}
	raw, _ := db.MarshalDexOfferCreation(meta)
	return &db.Transaction{
		ID: 42, Dex: db.DexBisq, Status: db.StatusDexFiatHasArrived,
		TradeMode: "hedgeless_double_collateral", BTCRequiredLiquidity: 0.02, CexBitcoinAmount: 0.018,
		HubAddress: "bc1qpool", BTCPoolRefillStatus: "withdrawal_initiated", DexOfferCreation: raw,
	}
}

func TestReleaseHappyPath(t *testing.T) {
	store := newFakeStore(fiatArrivedTx())
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{ReleaseTxID: "txabc"}}
	w := NewWorker(store, rel)

	if err := w.release(context.Background(), 42); err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.calls != 1 {
		t.Fatalf("releaser calls = %d, want 1", rel.calls)
	}
	if rel.last.Exchange != "hodlhodl" || rel.last.ContractID != "C123" {
		t.Fatalf("release input = %+v", rel.last)
	}
	tx := store.snapshot()
	if tx.Status != db.StatusDexPositionReleased {
		t.Fatalf("status = %q, want DexPositionReleased", tx.Status)
	}
	if tx.DexBtcRelease.IsZero() {
		t.Fatal("DexBtcRelease not set")
	}
	var meta db.DexOfferMeta
	_ = json.Unmarshal(tx.DexOfferCreation, &meta)
	if meta.Execution["release_tx_id"] != "txabc" {
		t.Fatalf("release_tx_id not recorded: %v", meta.Execution["release_tx_id"])
	}
}

func TestReleaseIdempotentWhenAlreadyReleased(t *testing.T) {
	tx := fiatArrivedTx()
	tx.DexBtcRelease = time.Now().UTC() // already released
	store := newFakeStore(tx)
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{}}
	w := NewWorker(store, rel)

	if err := w.release(context.Background(), 42); err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.calls != 0 {
		t.Fatalf("releaser called %d times on already-released tx, want 0", rel.calls)
	}
	if store.updateCount() != 0 {
		t.Fatalf("store updated %d times, want 0", store.updateCount())
	}
}

func TestHedgelessReleaseReturnsDepositAfterPersistingRelease(t *testing.T) {
	tx := hedgelessBisqFiatArrivedTx()
	store := newFakeStore(tx)
	rel := &fakeReleaser{
		result:       &dexaccount.ReleaseResult{ReleaseTxID: "payout-tx"},
		returnResult: &dexaccount.SecurityDepositReturnResult{Method: "bisq_withdraw_funds", TxID: "deposit-tx"},
	}
	w := NewWorker(store, rel)

	if err := w.release(context.Background(), tx.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.calls != 1 || rel.returnCalls != 1 {
		t.Fatalf("release/return calls = %d/%d, want 1/1", rel.calls, rel.returnCalls)
	}
	if rel.lastReturn.PoolAddress != "bc1qpool" || rel.lastReturn.Memo != "hedgeless-deposit-return-42" {
		t.Fatalf("return input = %+v", rel.lastReturn)
	}
	got := store.snapshot()
	if got.Status != db.StatusDexPositionReleased || got.DexBtcRelease.IsZero() {
		t.Fatalf("DEX release was not persisted before return completion: %+v", got)
	}
	if got.BTCPoolRefillStatus != "withdrawal_initiated" {
		t.Fatalf("deposit return overwrote in-flight refill status: %q", got.BTCPoolRefillStatus)
	}
	audit := readDepositReturnAudit(got)
	if audit.Status != "initiated" || audit.TxID != "deposit-tx" || math.Abs(audit.AmountBTC-0.002) > 1e-12 {
		t.Fatalf("deposit return audit = %+v", audit)
	}

	if err := w.release(context.Background(), tx.ID); err != nil {
		t.Fatalf("idempotent resume: %v", err)
	}
	if rel.calls != 1 || rel.returnCalls != 1 {
		t.Fatalf("idempotent release/return calls = %d/%d, want 1/1", rel.calls, rel.returnCalls)
	}
}

func TestHedgelessDepositReturnFailureNeedsReview(t *testing.T) {
	tx := hedgelessBisqFiatArrivedTx()
	store := newFakeStore(tx)
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{}, returnErr: fmt.Errorf("withdraw unavailable")}
	w := NewWorker(store, rel)
	w.maxRetries = 1

	if err := w.releaseWithRetry(context.Background(), tx.ID); err == nil {
		t.Fatal("expected return failure")
	}
	got := store.snapshot()
	if got.Status != db.StatusDexPositionReleased || got.BTCPoolRefillStatus != "needs_review" {
		t.Fatalf("failed return row = %+v", got)
	}
	if audit := readDepositReturnAudit(got); audit.Status != "needs_review" || audit.LastError == "" {
		t.Fatalf("deposit return audit = %+v", audit)
	}
}

func TestReleaseRejectsWrongStatus(t *testing.T) {
	tx := fiatArrivedTx()
	tx.Status = db.StatusDexPositionTaken // fiat not arrived yet
	store := newFakeStore(tx)
	rel := &fakeReleaser{}
	w := NewWorker(store, rel)

	if err := w.release(context.Background(), 42); err == nil {
		t.Fatal("expected error for non-releasable status, got nil")
	}
	if rel.calls != 0 {
		t.Fatalf("releaser called on wrong-status tx")
	}
}

func TestEnqueueAndRunReleases(t *testing.T) {
	store := newFakeStore(fiatArrivedTx())
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{ReleaseTxID: "tx1"}}
	w := NewWorker(store, rel)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("worker did not exit after cancel")
		}
	})

	if !w.Enqueue(42) {
		t.Fatal("enqueue returned false")
	}
	// Wait for the worker to signal the update instead of polling the
	// unsynchronized store field.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-store.updated:
			if tx := store.snapshot(); tx.Status == db.StatusDexPositionReleased {
				return
			}
		case <-timeout:
			t.Fatal("worker did not release the enqueued transaction in time")
		}
	}
}
