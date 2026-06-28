package dexrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
)

// fakeStore is an in-memory txStore.
type fakeStore struct {
	tx      *db.Transaction
	updates int
}

func (f *fakeStore) GetTransaction(id uint) (*db.Transaction, error) {
	if f.tx == nil || f.tx.ID != id {
		return nil, fmt.Errorf("transaction not found")
	}
	cp := *f.tx
	return &cp, nil
}

func (f *fakeStore) UpdateTransaction(tx *db.Transaction) error {
	f.updates++
	cp := *tx
	f.tx = &cp
	return nil
}

// fakeReleaser records calls and returns a canned result/error.
type fakeReleaser struct {
	calls  int
	last   dexaccount.ReleaseInput
	result *dexaccount.ReleaseResult
	err    error
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

func TestReleaseHappyPath(t *testing.T) {
	store := &fakeStore{tx: fiatArrivedTx()}
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
	if store.tx.Status != db.StatusDexPositionReleased {
		t.Fatalf("status = %q, want DexPositionReleased", store.tx.Status)
	}
	if store.tx.DexBtcRelease.IsZero() {
		t.Fatal("DexBtcRelease not set")
	}
	var meta db.DexOfferMeta
	_ = json.Unmarshal(store.tx.DexOfferCreation, &meta)
	if meta.Execution["release_tx_id"] != "txabc" {
		t.Fatalf("release_tx_id not recorded: %v", meta.Execution["release_tx_id"])
	}
}

func TestReleaseIdempotentWhenAlreadyReleased(t *testing.T) {
	tx := fiatArrivedTx()
	tx.DexBtcRelease = time.Now().UTC() // already released
	store := &fakeStore{tx: tx}
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{}}
	w := NewWorker(store, rel)

	if err := w.release(context.Background(), 42); err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.calls != 0 {
		t.Fatalf("releaser called %d times on already-released tx, want 0", rel.calls)
	}
	if store.updates != 0 {
		t.Fatalf("store updated %d times, want 0", store.updates)
	}
}

func TestReleaseRejectsWrongStatus(t *testing.T) {
	tx := fiatArrivedTx()
	tx.Status = db.StatusDexPositionTaken // fiat not arrived yet
	store := &fakeStore{tx: tx}
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
	store := &fakeStore{tx: fiatArrivedTx()}
	rel := &fakeReleaser{result: &dexaccount.ReleaseResult{ReleaseTxID: "tx1"}}
	w := NewWorker(store, rel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	if !w.Enqueue(42) {
		t.Fatal("enqueue returned false")
	}
	// Wait for the worker to process the job.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.tx.Status == db.StatusDexPositionReleased {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not release the enqueued transaction in time")
}
