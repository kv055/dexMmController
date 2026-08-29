package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"dexMmController/internal/db"
)

// dexReleaseEnqueuer hands a transaction id to the release worker (satisfied by
// *dexrelease.Worker). Enqueue is non-blocking and returns false when the queue is full.
type dexReleaseEnqueuer interface {
	Enqueue(txID uint) bool
}

// WithDexRelease wires the Stage-6 release webhook: the worker that performs releases and
// the shared bearer token the fiat-controller authenticates with. With either unset the
// endpoint responds 503.
func (s *Server) WithDexRelease(enqueuer dexReleaseEnqueuer, token string) *Server {
	s.dexRelease = enqueuer
	s.dexReleaseToken = token
	return s
}

// dexReleaseRequest is the webhook body sent by the fiat-controller once fiat has landed.
type dexReleaseRequest struct {
	TransactionID uint `json:"transaction_id"`
}

// handleDexReleaseWebhook is the sole trigger for Stage 6. The fiat-controller calls it
// (with the shared bearer token) when the fiat for a bid has arrived; we validate, enqueue
// the release, and return 202. Actual signing/broadcast happens asynchronously in the
// release worker, idempotently.
func (s *Server) handleDexReleaseWebhook(w http.ResponseWriter, r *http.Request) {
	if s.dexRelease == nil || strings.TrimSpace(s.dexReleaseToken) == "" {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("dex release webhook is not configured"))
		return
	}
	if !s.authorizeDexRelease(r) {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var body dexReleaseRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if body.TransactionID == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("transaction_id is required"))
		return
	}

	tx, err := s.repo.GetTransaction(body.TransactionID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "transaction not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}

	depositReturnPending := hedgelessDepositReturnPending(tx)
	// A released hedgeless transaction remains actionable until its security
	// deposit has restored the pool. Repeated fiat-controller webhooks can safely
	// resume that idempotent return step.
	if (!tx.DexBtcRelease.IsZero() || tx.Status == db.StatusDexPositionReleased || tx.TransactionComplete) && !depositReturnPending {
		writeJSON(w, http.StatusOK, map[string]any{"status": "already_released", "transaction_id": tx.ID})
		return
	}

	if !s.dexRelease.Enqueue(tx.ID) {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("release queue is full; retry shortly"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "transaction_id": tx.ID})
}

func hedgelessDepositReturnPending(tx *db.Transaction) bool {
	return tx != nil && tx.TradeMode == "hedgeless_double_collateral" &&
		tx.BTCPoolRefillStatus != "" && tx.BTCPoolRefillStatus != "restored" && tx.BTCPoolRefillStatus != "needs_review"
}

// authorizeDexRelease checks the Authorization: Bearer <token> header against the configured
// shared secret in constant time.
func (s *Server) authorizeDexRelease(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.dexReleaseToken)) == 1
}
