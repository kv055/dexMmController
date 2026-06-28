// Package dexrelease implements Stage 6 of the bid journey: releasing the escrowed BTC to
// the counterparty buyer. It is a standalone worker fed solely by the fiat-controller
// webhook (no periodic poll, no healthcheck involvement) — the fiat controller owns
// stages 4–5 and tells us when the fiat has landed.
package dexrelease

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
)

const (
	defaultQueueSize  = 256
	defaultMaxRetries = 3
	retryBackoff      = 3 * time.Second
)

// txStore is the transaction-row access the worker needs (satisfied by
// *db.TransactionRepository).
type txStore interface {
	GetTransaction(id uint) (*db.Transaction, error)
	UpdateTransaction(tx *db.Transaction) error
}

// releaser performs the venue release (satisfied by *dexaccount.Releaser).
type releaser interface {
	Release(ctx context.Context, in dexaccount.ReleaseInput) (*dexaccount.ReleaseResult, error)
}

// Worker drains a queue of transaction ids and releases each one's escrowed BTC. Jobs are
// produced only by Enqueue (called by the webhook handler); a single goroutine consumes
// them serially so concurrent signing can't race.
type Worker struct {
	repo       txStore
	releaser   releaser
	jobs       chan uint
	maxRetries int
}

// NewWorker creates a release worker.
func NewWorker(repo txStore, rel releaser) *Worker {
	return &Worker{
		repo:       repo,
		releaser:   rel,
		jobs:       make(chan uint, defaultQueueSize),
		maxRetries: defaultMaxRetries,
	}
}

// Enqueue submits a transaction id for release. It is non-blocking; it returns false if the
// queue is full (the fiat controller should retry — release is idempotent).
func (w *Worker) Enqueue(txID uint) bool {
	select {
	case w.jobs <- txID:
		return true
	default:
		return false
	}
}

// Run consumes release jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case txID := <-w.jobs:
			if err := w.releaseWithRetry(ctx, txID); err != nil {
				log.Printf("dexrelease: tx %d release failed after retries: %v", txID, err)
			}
		}
	}
}

// releaseWithRetry retries transient failures a few times; the row is left untouched on
// persistent failure so a re-sent webhook can drive it again (release is idempotent).
func (w *Worker) releaseWithRetry(ctx context.Context, txID uint) error {
	var lastErr error
	for attempt := 1; attempt <= w.maxRetries; attempt++ {
		if err := w.release(ctx, txID); err != nil {
			lastErr = err
			log.Printf("dexrelease: tx %d release attempt %d/%d: %v", txID, attempt, w.maxRetries, err)
			if attempt < w.maxRetries {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryBackoff):
				}
			}
			continue
		}
		return nil
	}
	return lastErr
}

// release performs one idempotent release for a transaction id.
func (w *Worker) release(ctx context.Context, txID uint) error {
	tx, err := w.repo.GetTransaction(txID)
	if err != nil {
		return fmt.Errorf("load transaction: %w", err)
	}

	// Idempotency: already released (timestamp set or status advanced) → nothing to do.
	if !tx.DexBtcRelease.IsZero() || tx.Status == db.StatusDexPositionReleased || tx.TransactionComplete {
		return nil
	}
	// Only release once the fiat controller has advanced the row to fiat-arrived.
	if tx.Status != db.StatusDexFiatHasArrived {
		return fmt.Errorf("transaction %d not releasable in status %q", txID, tx.Status)
	}

	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return fmt.Errorf("parse DexOfferCreation: %w", err)
	}
	exchange := deriveExchange(tx, meta)
	contractID := meta.ContractID()
	if contractID == "" {
		return fmt.Errorf("transaction %d has no contract_id in execution", txID)
	}

	result, err := w.releaser.Release(ctx, dexaccount.ReleaseInput{Exchange: exchange, ContractID: contractID})
	if err != nil {
		return fmt.Errorf("release on %s: %w", exchange, err)
	}

	// Record + advance. DexBtcRelease doubles as the idempotency guard above.
	tx.DexBtcRelease = time.Now().UTC()
	tx.Status = db.StatusDexPositionReleased
	if meta.Execution == nil {
		meta.Execution = map[string]any{}
	}
	if result.ReleaseTxID != "" {
		meta.Execution["release_tx_id"] = result.ReleaseTxID
	}
	meta.Execution["released_at"] = tx.DexBtcRelease.Format(time.RFC3339)
	if raw, mErr := db.MarshalDexOfferCreation(meta); mErr == nil {
		tx.DexOfferCreation = raw
	}
	if err := w.repo.UpdateTransaction(tx); err != nil {
		return fmt.Errorf("persist release: %w", err)
	}
	log.Printf("dexrelease: tx %d released on %s (release_tx=%s)", txID, exchange, result.ReleaseTxID)
	return nil
}

// deriveExchange resolves the DEX venue slug from offer metadata, falling back to the
// transaction's Dex column (which carries no bisq vs bisq2 distinction).
func deriveExchange(tx *db.Transaction, meta db.DexOfferMeta) string {
	if ex := strings.TrimSpace(meta.Exchange()); ex != "" {
		return ex
	}
	switch tx.Dex {
	case db.DexHodlHodl:
		return "hodlhodl"
	case db.DexBisq:
		return "bisq"
	case db.DexRoboSats:
		return "robosats"
	default:
		return strings.ToLower(string(tx.Dex))
	}
}
