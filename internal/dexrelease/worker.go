// Package dexrelease implements Stage 6 of the bid journey: releasing the escrowed BTC to
// the counterparty buyer. It is a standalone worker fed solely by the fiat-controller
// webhook (no periodic poll, no healthcheck involvement) — the fiat controller owns
// stages 4–5 and tells us when the fiat has landed.
package dexrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
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

type securityDepositReturner interface {
	ReturnSecurityDeposit(ctx context.Context, in dexaccount.SecurityDepositReturnInput) (*dexaccount.SecurityDepositReturnResult, error)
}

// Worker drains a queue of transaction ids and releases each one's escrowed BTC. Jobs are
// produced only by Enqueue (called by the webhook handler); a single goroutine consumes
// them serially so concurrent signing can't race.
type Worker struct {
	repo       txStore
	releaser   releaser
	returner   securityDepositReturner
	jobs       chan uint
	maxRetries int
}

// NewWorker creates a release worker.
func NewWorker(repo txStore, rel releaser) *Worker {
	w := &Worker{
		repo:       repo,
		releaser:   rel,
		jobs:       make(chan uint, defaultQueueSize),
		maxRetries: defaultMaxRetries,
	}
	if returner, ok := rel.(securityDepositReturner); ok {
		w.returner = returner
	}
	return w
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

// releaseWithRetry retries transient failures a few times. A persistent
// post-release collateral-return failure is parked for operator review; failures
// before the DEX release leave the row retryable by a later webhook.
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
	if err := w.markDepositReturnNeedsReview(txID, lastErr); err != nil {
		return fmt.Errorf("%v; persist security-deposit review state: %w", lastErr, err)
	}
	return lastErr
}

// release performs one idempotent release for a transaction id.
func (w *Worker) release(ctx context.Context, txID uint) error {
	tx, err := w.repo.GetTransaction(txID)
	if err != nil {
		return fmt.Errorf("load transaction: %w", err)
	}

	if tx.TransactionComplete {
		return nil
	}
	securityDepositBTC := hedgelessSecurityDepositBTC(tx)
	needsDepositReturn := tx.TradeMode == "hedgeless_double_collateral" && securityDepositBTC > 1e-12
	releaseDone := !tx.DexBtcRelease.IsZero() || tx.Status == db.StatusDexPositionReleased
	if releaseDone && (!needsDepositReturn || tx.BTCPoolRefillStatus == "restored" || tx.BTCPoolRefillStatus == "needs_review") {
		return nil
	}
	// Only release once the fiat controller has advanced the row to fiat-arrived.
	if !releaseDone && tx.Status != db.StatusDexFiatHasArrived {
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

	if !releaseDone {
		result, err := w.releaser.Release(ctx, dexaccount.ReleaseInput{Exchange: exchange, ContractID: contractID, RequestID: uint64(tx.ID)})
		if err != nil {
			return fmt.Errorf("release on %s: %w", exchange, err)
		}

		// Persist the DEX release before initiating the separate collateral
		// return. A retry can then resume without re-broadcasting the release.
		latest, err := w.repo.GetTransaction(tx.ID)
		if err != nil {
			return fmt.Errorf("reload transaction after release: %w", err)
		}
		tx = latest
		meta, err = db.ParseDexOfferCreation(tx.DexOfferCreation)
		if err != nil {
			return fmt.Errorf("parse latest DexOfferCreation: %w", err)
		}
		tx.DexBtcRelease = time.Now().UTC()
		tx.SetStatus(db.StatusDexPositionReleased, "")
		if meta.Execution == nil {
			meta.Execution = map[string]any{}
		}
		if result != nil && result.ReleaseTxID != "" {
			meta.Execution["release_tx_id"] = result.ReleaseTxID
		}
		meta.Execution["released_at"] = tx.DexBtcRelease.Format(time.RFC3339)
		if raw, mErr := db.MarshalDexOfferCreation(meta); mErr == nil {
			tx.DexOfferCreation = raw
		}
		if err := w.repo.UpdateTransaction(tx); err != nil {
			return fmt.Errorf("persist release: %w", err)
		}
		releaseTxID := ""
		if result != nil {
			releaseTxID = result.ReleaseTxID
		}
		log.Printf("dexrelease: tx %d released on %s (release_tx=%s)", txID, exchange, releaseTxID)
	}
	if !needsDepositReturn {
		return nil
	}
	return w.returnSecurityDeposit(ctx, tx, exchange, contractID, securityDepositBTC)
}

type depositReturnAudit struct {
	Method      string    `json:"method,omitempty"`
	Status      string    `json:"status,omitempty"`
	Destination string    `json:"destination,omitempty"`
	AmountBTC   float64   `json:"amount_btc,omitempty"`
	TxID        string    `json:"txid,omitempty"`
	Memo        string    `json:"memo,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

func hedgelessSecurityDepositBTC(tx *db.Transaction) float64 {
	if tx == nil {
		return 0
	}
	return math.Max(0, tx.BTCRequiredLiquidity-tx.CexBitcoinAmount)
}

func updateDepositReturnAudit(tx *db.Transaction, mutate func(*depositReturnAudit)) error {
	var details map[string]json.RawMessage
	if len(tx.BTCPoolRefillDetails) > 0 {
		if err := json.Unmarshal(tx.BTCPoolRefillDetails, &details); err != nil {
			return err
		}
	}
	if details == nil {
		details = make(map[string]json.RawMessage)
	}
	var audit depositReturnAudit
	if raw := details["security_deposit_return"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &audit)
	}
	mutate(&audit)
	raw, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	details["security_deposit_return"] = raw
	tx.BTCPoolRefillDetails, err = json.Marshal(details)
	return err
}

func readDepositReturnAudit(tx *db.Transaction) depositReturnAudit {
	var details struct {
		SecurityDepositReturn depositReturnAudit `json:"security_deposit_return"`
	}
	_ = json.Unmarshal(tx.BTCPoolRefillDetails, &details)
	return details.SecurityDepositReturn
}

func (w *Worker) returnSecurityDeposit(ctx context.Context, tx *db.Transaction, exchange, contractID string, amountBTC float64) error {
	if w.returner == nil {
		return fmt.Errorf("security-deposit return is not configured")
	}
	latest, err := w.repo.GetTransaction(tx.ID)
	if err != nil {
		return fmt.Errorf("reload transaction before security-deposit return: %w", err)
	}
	tx = latest
	if audit := readDepositReturnAudit(tx); audit.Status == "initiated" || audit.Status == "returned" {
		return nil
	}
	destination := strings.TrimSpace(tx.HubAddress)
	if destination == "" {
		meta, _ := db.ParseDexOfferCreation(tx.DexOfferCreation)
		destination = strings.TrimSpace(meta.LNDReceiveAddress)
	}
	if destination == "" {
		return fmt.Errorf("transaction %d has no LND pool address", tx.ID)
	}
	memo := fmt.Sprintf("hedgeless-deposit-return-%d", tx.ID)
	if err := updateDepositReturnAudit(tx, func(a *depositReturnAudit) {
		a.Status = "pending"
		a.Destination = destination
		a.AmountBTC = amountBTC
		a.Memo = memo
		a.Attempts++
		a.LastError = ""
		a.UpdatedAt = time.Now().UTC()
	}); err != nil {
		return err
	}
	if err := w.repo.UpdateTransaction(tx); err != nil {
		return fmt.Errorf("persist security-deposit return intent: %w", err)
	}

	result, err := w.returner.ReturnSecurityDeposit(ctx, dexaccount.SecurityDepositReturnInput{
		Exchange: exchange, ContractID: contractID, RequestID: uint64(tx.ID), PoolAddress: destination, Memo: memo,
	})
	if err != nil {
		if latest, reloadErr := w.repo.GetTransaction(tx.ID); reloadErr == nil {
			tx = latest
		}
		_ = updateDepositReturnAudit(tx, func(a *depositReturnAudit) {
			a.LastError = err.Error()
			a.UpdatedAt = time.Now().UTC()
		})
		_ = w.repo.UpdateTransaction(tx)
		return fmt.Errorf("return security deposit on %s: %w", exchange, err)
	}
	latest, err = w.repo.GetTransaction(tx.ID)
	if err != nil {
		return fmt.Errorf("reload transaction after security-deposit return: %w", err)
	}
	tx = latest
	if err := updateDepositReturnAudit(tx, func(a *depositReturnAudit) {
		a.Status = "initiated"
		a.LastError = ""
		a.UpdatedAt = time.Now().UTC()
		if result != nil {
			a.Method = result.Method
			a.TxID = result.TxID
		}
	}); err != nil {
		return err
	}
	if err := w.repo.UpdateTransaction(tx); err != nil {
		return fmt.Errorf("persist security-deposit return receipt: %w", err)
	}
	return nil
}

func (w *Worker) markDepositReturnNeedsReview(txID uint, cause error) error {
	tx, err := w.repo.GetTransaction(txID)
	if err != nil || tx == nil || tx.TradeMode != "hedgeless_double_collateral" || tx.DexBtcRelease.IsZero() {
		return err
	}
	if err := updateDepositReturnAudit(tx, func(a *depositReturnAudit) {
		a.Status = "needs_review"
		if cause != nil {
			a.LastError = cause.Error()
		}
		a.UpdatedAt = time.Now().UTC()
	}); err != nil {
		return err
	}
	tx.BTCPoolRefillStatus = "needs_review"
	return w.repo.UpdateTransaction(tx)
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
	case db.DexBisq2:
		return "bisq2"
	case db.DexRoboSats:
		return "robosats"
	default:
		return strings.ToLower(string(tx.Dex))
	}
}
