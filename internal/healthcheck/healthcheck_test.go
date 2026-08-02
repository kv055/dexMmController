package healthcheck

import (
	"context"
	"testing"
	"time"

	"dexMmController/internal/db"
)

// fakeRecoverer records which recovery entrypoint was dispatched.
type fakeRecoverer struct {
	started    int
	withdrawal int
	hedge      int
}

func (f *fakeRecoverer) ResumeFromTradeflowStarted(_ context.Context, _ *db.Transaction) error {
	f.started++
	return nil
}

func (f *fakeRecoverer) ResumeWithdrawal(_ context.Context, _ *db.Transaction) error {
	f.withdrawal++
	return nil
}

func (f *fakeRecoverer) RecoverHedge(_ context.Context, _ *db.Transaction) error {
	f.hedge++
	return nil
}

func newChecker(rec TradeflowRecoverer) *HealthChecker {
	return &HealthChecker{recoverer: rec, recoveryGracePeriod: time.Minute}
}

func staleRow(status db.TransactionStatus) *db.Transaction {
	tx := &db.Transaction{Status: status}
	tx.UpdatedAt = time.Now().Add(-10 * time.Minute) // older than the grace period
	return tx
}

func freshRow(status db.TransactionStatus) *db.Transaction {
	tx := &db.Transaction{Status: status}
	tx.UpdatedAt = time.Now() // within the grace period
	return tx
}

func TestVerify_DispatchesToRecoverer(t *testing.T) {
	rec := &fakeRecoverer{}
	h := newChecker(rec)

	if err := h.verify(context.Background(), staleRow(db.StatusCexTradeflowStarted)); err != nil {
		t.Fatalf("CexTradeflowStarted: %v", err)
	}
	if err := h.verify(context.Background(), staleRow(db.StatusCexTradeSuccessful)); err != nil {
		t.Fatalf("CexTradeSuccessful: %v", err)
	}
	if err := h.verify(context.Background(), staleRow(db.StatusHedgeFailedNeedsReview)); err != nil {
		t.Fatalf("HedgeFailedNeedsReview: %v", err)
	}

	if rec.started != 1 {
		t.Errorf("ResumeFromTradeflowStarted: want 1 call, got %d", rec.started)
	}
	if rec.withdrawal != 1 {
		t.Errorf("ResumeWithdrawal: want 1 call, got %d", rec.withdrawal)
	}
	if rec.hedge != 1 {
		t.Errorf("RecoverHedge: want 1 call, got %d", rec.hedge)
	}
}

func TestVerify_GracePeriodSkipsFreshRows(t *testing.T) {
	rec := &fakeRecoverer{}
	h := newChecker(rec)

	if err := h.verify(context.Background(), freshRow(db.StatusCexTradeflowStarted)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := h.verify(context.Background(), freshRow(db.StatusCexTradeSuccessful)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if rec.started != 0 || rec.withdrawal != 0 {
		t.Errorf("fresh rows must be skipped (started=%d withdrawal=%d)", rec.started, rec.withdrawal)
	}
}

func TestVerify_NeedsReviewIsNoOp(t *testing.T) {
	rec := &fakeRecoverer{}
	h := newChecker(rec)

	if err := h.verify(context.Background(), staleRow(db.StatusCexTradeflowNeedsReview)); err != nil {
		t.Fatalf("CexTradeflowNeedsReview should be a no-op, got %v", err)
	}
	if rec.started+rec.withdrawal+rec.hedge != 0 {
		t.Errorf("NeedsReview must not trigger any recovery call")
	}
}

func TestVerify_HedgelessFundingStatesAreOwnedByRefillWorker(t *testing.T) {
	h := newChecker(&fakeRecoverer{})
	for _, status := range []db.TransactionStatus{db.StatusHedgelessTradeStarted, db.StatusHedgelessFundingInitiated, db.StatusHedgelessFundingNeedsReview} {
		if err := h.verify(context.Background(), staleRow(status)); err != nil {
			t.Fatalf("status %s should be a no-op: %v", status, err)
		}
	}
}

func TestVerify_HedgeReviewIgnoresGracePeriod(t *testing.T) {
	rec := &fakeRecoverer{}
	h := newChecker(rec)

	// Even a fresh HedgeFailedNeedsReview row is actionable (Run has already returned).
	if err := h.verify(context.Background(), freshRow(db.StatusHedgeFailedNeedsReview)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if rec.hedge != 1 {
		t.Errorf("RecoverHedge should run regardless of grace period, got %d", rec.hedge)
	}
}
