package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dexTakerController/internal/bitcoin"
	"dexTakerController/internal/db"
	"dexTakerController/internal/dexaccount"
	"dexTakerController/internal/exchangeconnectors/dex/bisq"
	"dexTakerController/internal/exchangeconnectors/dex/hodlhodl"
)

// fakeRecoverer records which recovery entrypoint was dispatched.
type fakeRecoverer struct {
	started    int
	withdrawal int
	hedge      int
}

type fakeBisqPositionSource struct {
	trade *bisq.Trade
	err   error
	calls int
}

type fakeHodlPositionSource struct {
	contract *hodlhodl.Contract
	err      error
	calls    int
}

func (f *fakeHodlPositionSource) GetContract(_ context.Context, _ string) (*hodlhodl.Contract, error) {
	f.calls++
	return f.contract, f.err
}

func (f *fakeHodlPositionSource) IsContractCompleted(context.Context, string) (bool, error) {
	return false, nil
}

type fakeBitcoinTransactionSource struct {
	status *bitcoin.TxStatus
	err    error
	calls  int
	txid   string
}

func (f *fakeBitcoinTransactionSource) FetchTransaction(_ context.Context, txid string) (*bitcoin.TxStatus, error) {
	f.calls++
	f.txid = txid
	return f.status, f.err
}

type fakeTransactionStore struct {
	transactions []db.Transaction
	updated      *db.Transaction
	updates      int
	loadErr      error
	updateErr    error
}

func (f *fakeTransactionStore) GetTransactionsForHealthcheck() ([]db.Transaction, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return append([]db.Transaction(nil), f.transactions...), nil
}

func (f *fakeTransactionStore) UpdateTransaction(tx *db.Transaction) error {
	f.updates++
	cp := *tx
	f.updated = &cp
	return f.updateErr
}

func (f *fakeBisqPositionSource) GetPosition(_ context.Context, _ string) (*bisq.Trade, error) {
	f.calls++
	return f.trade, f.err
}

type fakeStructuredPaymentResolver struct {
	payloads []json.RawMessage
	errs     []error
	calls    int
}

func (f *fakeStructuredPaymentResolver) IsStructuredVenue(string) bool { return true }
func (f *fakeStructuredPaymentResolver) FetchCounterparty(context.Context, string, map[string]any) (json.RawMessage, error) {
	i := f.calls
	f.calls++
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	var payload json.RawMessage
	if i < len(f.payloads) {
		payload = f.payloads[i]
	}
	return payload, err
}
func (f *fakeStructuredPaymentResolver) CaptureChatPayment(context.Context, dexaccount.ChatRequest) (json.RawMessage, error) {
	return nil, nil
}

type anomalyCall struct {
	reason string
	detail string
}

type fakeAnomalyNotifier struct {
	enabled bool
	err     error
	calls   []anomalyCall
}

func (f *fakeAnomalyNotifier) Enabled() bool { return f.enabled }

func (f *fakeAnomalyNotifier) NotifyAnomaly(_ context.Context, _, _, reason, detail string) error {
	f.calls = append(f.calls, anomalyCall{reason: reason, detail: detail})
	return f.err
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

func TestVerifyDexPositionTakenDoesNotTreatVenuePaidAsFiatArrival(t *testing.T) {
	store := &fakeTransactionStore{}
	meta, err := db.MarshalDexOfferCreation(db.DexOfferMeta{Execution: map[string]any{
		"exchange":    "hodlhodl",
		"contract_id": "contract-1",
	}})
	if err != nil {
		t.Fatalf("MarshalDexOfferCreation: %v", err)
	}
	tx := &db.Transaction{
		ID:               42,
		Dex:              db.DexHodlHodl,
		Status:           db.StatusDexPositionTaken,
		DexOfferCreation: meta,
	}
	hodl := &fakeHodlPositionSource{contract: &hodlhodl.Contract{ID: "contract-1", Status: hodlhodl.ContractPaid}}
	h := &HealthChecker{repo: store, hodlhodl: hodl}

	if err := h.verifyDexPositionTaken(context.Background(), tx); err != nil {
		t.Fatalf("verifyDexPositionTaken: %v", err)
	}
	if hodl.calls != 1 {
		t.Fatalf("GetContract calls = %d, want 1", hodl.calls)
	}
	if store.updates != 0 {
		t.Fatalf("venue paid flag caused %d transaction updates; only bank evidence may advance status", store.updates)
	}
	if tx.Status != db.StatusDexPositionTaken {
		t.Fatalf("status = %q, want %q", tx.Status, db.StatusDexPositionTaken)
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

func bisqTakenTransaction(t *testing.T) *db.Transaction {
	t.Helper()
	meta, err := db.MarshalDexOfferCreation(db.DexOfferMeta{
		Execution: map[string]any{"exchange": "bisq", "trade_id": "trade-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &db.Transaction{
		Dex:              db.DexBisq,
		Status:           db.StatusDexPositionTaken,
		FiatAmount:       123.45,
		FiatCurrency:     "EUR",
		DexOfferCreation: meta,
	}
}

func TestVerifyBisqPositionTakenEscalatesAfterObservationDelayOnce(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	source := &fakeBisqPositionSource{trade: &bisq.Trade{
		Phase: "FIAT_SENT",
		State: "SELLER_RECEIVED_FIAT_PAYMENT_INITIATED_MSG",
	}}
	notifier := &fakeAnomalyNotifier{enabled: true}
	h := &HealthChecker{
		bisq:                    source,
		anomaly:                 notifier,
		bisqFiatEscalationDelay: 2 * time.Hour,
		bisqFiatEscalations:     make(map[string]bisqFiatEscalationState),
		now:                     func() time.Time { return now },
	}
	tx := bisqTakenTransaction(t)

	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(90 * time.Minute)
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("alerted before delay: %d calls", len(notifier.calls))
	}

	now = now.Add(30 * time.Minute)
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("alerts after delay = %d, want 1", len(notifier.calls))
	}
	if tx.Status != db.StatusDexPositionTaken {
		t.Fatalf("status mutated to %s", tx.Status)
	}

	now = now.Add(time.Hour)
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("dedupe failed: %d alerts", len(notifier.calls))
	}
}

func TestVerifyBisqPositionTakenRetriesPendingPaymentPayload(t *testing.T) {
	store := &fakeTransactionStore{}
	resolver := &fakeStructuredPaymentResolver{
		errs:     []error{errors.New("bisq: maker payment payload is pending"), nil},
		payloads: []json.RawMessage{nil, json.RawMessage(`{"iban":"DE00"}`)},
	}
	h := &HealthChecker{
		repo:     store,
		resolver: resolver,
		bisq:     &fakeBisqPositionSource{trade: &bisq.Trade{State: "DEPOSIT_CONFIRMED"}},
	}
	tx := bisqTakenTransaction(t)

	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.CounterpartyPaymentInfo) != 0 || store.updates != 0 {
		t.Fatal("pending payload must not be stored as payment details")
	}

	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 2 {
		t.Fatalf("calls = %d, want 2", resolver.calls)
	}
	if string(tx.CounterpartyPaymentInfo) != `{"iban":"DE00"}` {
		t.Fatalf("stored = %s", tx.CounterpartyPaymentInfo)
	}
	if store.updates != 1 {
		t.Fatalf("updates = %d, want 1", store.updates)
	}
}

func TestVerifyBisqPositionTakenWaitsForEnabledNotifierAndRetriesFailures(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	source := &fakeBisqPositionSource{trade: &bisq.Trade{IsPaymentStartedMessageSent: true}}
	notifier := &fakeAnomalyNotifier{}
	h := &HealthChecker{
		bisq:                    source,
		anomaly:                 notifier,
		bisqFiatEscalationDelay: time.Hour,
		bisqFiatEscalations:     make(map[string]bisqFiatEscalationState),
		now:                     func() time.Time { return now },
	}
	tx := bisqTakenTransaction(t)

	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 0 {
		t.Fatal("disabled notifier should not consume the escalation")
	}

	notifier.enabled = true
	notifier.err = errors.New("telegram unavailable")
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err == nil {
		t.Fatal("expected notifier error")
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("failed notification calls = %d, want 1", len(notifier.calls))
	}

	notifier.err = nil
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 2 {
		t.Fatalf("failed notification was not retried: %d calls", len(notifier.calls))
	}
}

func TestVerifyBisqPositionTakenDisabledOrMissingDependencies(t *testing.T) {
	tx := bisqTakenTransaction(t)
	source := &fakeBisqPositionSource{trade: &bisq.Trade{Phase: "FIAT_SENT"}}
	notifier := &fakeAnomalyNotifier{enabled: true}
	h := &HealthChecker{
		bisq:                    source,
		anomaly:                 notifier,
		bisqFiatEscalationDelay: 0,
	}
	if err := h.verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 0 {
		t.Fatal("zero delay should disable escalation")
	}
	if err := (&HealthChecker{}).verifyBisqPositionTaken(context.Background(), tx); err != nil {
		t.Fatalf("missing dependencies should be a no-op: %v", err)
	}
	h = NewHealthChecker(nil, nil, nil, nil).
		WithTelegramRelay(nil, nil).
		WithBisq(nil)
	if h.anomaly != nil || h.bisq != nil {
		t.Fatal("nil builder dependencies must remain nil interfaces")
	}
}

func TestVerifyBisqPositionTakenUsesOneFetchAndPrioritizesFailure(t *testing.T) {
	source := &fakeBisqPositionSource{trade: &bisq.Trade{
		IsPaymentStartedMessageSent: true,
		HasFailed:                   true,
		ErrorMessage:                "protocol failed",
	}}
	notifier := &fakeAnomalyNotifier{enabled: true}
	h := &HealthChecker{
		bisq:                    source,
		anomaly:                 notifier,
		bisqFiatEscalationDelay: time.Nanosecond,
	}
	if err := h.verifyBisqPositionTaken(context.Background(), bisqTakenTransaction(t)); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 {
		t.Fatalf("GetPosition calls = %d, want 1", source.calls)
	}
	if len(notifier.calls) != 1 || notifier.calls[0].reason != "bisq trade failure" {
		t.Fatalf("unexpected alerts: %#v", notifier.calls)
	}
}

func bisqReleasedTransaction(t *testing.T, persistedReleaseTxID string) *db.Transaction {
	t.Helper()
	execution := map[string]any{"exchange": "bisq", "trade_id": "trade-1"}
	if persistedReleaseTxID != "" {
		execution["release_tx_id"] = persistedReleaseTxID
	}
	meta, err := db.MarshalDexOfferCreation(db.DexOfferMeta{Execution: execution})
	if err != nil {
		t.Fatal(err)
	}
	return &db.Transaction{
		ID:               42,
		Dex:              db.DexBisq,
		Status:           db.StatusDexPositionReleased,
		DexOfferCreation: meta,
	}
}

func TestRunVerifiesReleasedBisqTransactionVisibleInMempool(t *testing.T) {
	tx := bisqReleasedTransaction(t, "")
	store := &fakeTransactionStore{transactions: []db.Transaction{*tx}}
	source := &fakeBisqPositionSource{trade: &bisq.Trade{
		TradeID:           "trade-1",
		IsPayoutPublished: true,
		PayoutTxID:        "payout-1",
	}}
	chain := &fakeBitcoinTransactionSource{status: &bitcoin.TxStatus{
		TxID:        "payout-1",
		StatusLabel: "mempool",
	}}
	verifiedAt := time.Date(2026, time.August, 26, 16, 0, 0, 0, time.UTC)
	h := &HealthChecker{
		repo:    store,
		bisq:    source,
		btc:     chain,
		anomaly: &fakeAnomalyNotifier{enabled: true},
		now:     func() time.Time { return verifiedAt },
	}

	if err := h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || chain.calls != 1 || store.updates != 1 {
		t.Fatalf("calls: bisq=%d chain=%d updates=%d", source.calls, chain.calls, store.updates)
	}
	if store.updated == nil || !store.updated.TransactionComplete || store.updated.Status != db.StatusDexPositionReleased {
		t.Fatalf("unexpected updated transaction: %+v", store.updated)
	}
	meta, err := db.ParseDexOfferCreation(store.updated.DexOfferCreation)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Execution["release_tx_id"] != "payout-1" || meta.Execution["release_chain_status"] != "mempool" {
		t.Fatalf("verification metadata: %#v", meta.Execution)
	}
	if meta.Execution["release_verified_at"] != verifiedAt.Format(time.RFC3339) {
		t.Fatalf("release_verified_at = %#v", meta.Execution["release_verified_at"])
	}
}

func TestVerifyBisqPositionReleasedAcceptsConfirmedMatchingPayout(t *testing.T) {
	tx := bisqReleasedTransaction(t, "PAYOUT-1")
	store := &fakeTransactionStore{}
	source := &fakeBisqPositionSource{trade: &bisq.Trade{Phase: "WITHDRAWN", PayoutTxID: "payout-1"}}
	chain := &fakeBitcoinTransactionSource{status: &bitcoin.TxStatus{
		TxID:          "payout-1",
		Confirmed:     true,
		Confirmations: 3,
	}}
	h := &HealthChecker{repo: store, bisq: source, btc: chain}

	if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if store.updates != 1 || !tx.TransactionComplete {
		t.Fatalf("updates=%d complete=%v", store.updates, tx.TransactionComplete)
	}
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Execution["release_chain_status"] != "confirmed" {
		t.Fatalf("chain status = %#v", meta.Execution["release_chain_status"])
	}
	if got, ok := meta.Execution["release_confirmations"].(float64); !ok || got != 3 {
		t.Fatalf("confirmations = %#v", meta.Execution["release_confirmations"])
	}
}

func TestVerifyBisqPositionReleasedWaitsForCompleteEvidence(t *testing.T) {
	tests := []struct {
		name   string
		trade  *bisq.Trade
		status *bitcoin.TxStatus
	}{
		{name: "payout not published", trade: &bisq.Trade{Phase: "FIAT_RECEIVED", PayoutTxID: "payout-1"}},
		{name: "payout txid missing", trade: &bisq.Trade{IsPayoutPublished: true}},
		{name: "chain transaction missing", trade: &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "payout-1"}},
		{name: "chain transaction not found", trade: &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "payout-1"}, status: &bitcoin.TxStatus{TxID: "payout-1", StatusLabel: "not_found"}},
		{name: "unexpected chain status", trade: &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "payout-1"}, status: &bitcoin.TxStatus{TxID: "payout-1", StatusLabel: "unknown"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := bisqReleasedTransaction(t, "")
			store := &fakeTransactionStore{}
			chain := &fakeBitcoinTransactionSource{status: test.status}
			h := &HealthChecker{
				repo: store,
				bisq: &fakeBisqPositionSource{trade: test.trade},
				btc:  chain,
			}
			if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
			if store.updates != 0 || tx.TransactionComplete {
				t.Fatalf("unexpected completion: updates=%d complete=%v", store.updates, tx.TransactionComplete)
			}
		})
	}
}

func TestVerifyBisqPositionReleasedPropagatesLookupErrors(t *testing.T) {
	t.Run("bisq lookup", func(t *testing.T) {
		h := &HealthChecker{
			repo: &fakeTransactionStore{},
			bisq: &fakeBisqPositionSource{err: errors.New("daemon unavailable")},
			btc:  &fakeBitcoinTransactionSource{},
		}
		if err := h.verifyBisqPositionReleased(context.Background(), bisqReleasedTransaction(t, "")); err == nil {
			t.Fatal("expected Bisq lookup error")
		}
	})

	t.Run("bitcoin lookup", func(t *testing.T) {
		store := &fakeTransactionStore{}
		h := &HealthChecker{
			repo: store,
			bisq: &fakeBisqPositionSource{trade: &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "payout-1"}},
			btc:  &fakeBitcoinTransactionSource{err: errors.New("chain API unavailable")},
		}
		if err := h.verifyBisqPositionReleased(context.Background(), bisqReleasedTransaction(t, "")); err == nil {
			t.Fatal("expected Bitcoin lookup error")
		}
		if store.updates != 0 {
			t.Fatalf("updates after lookup error = %d", store.updates)
		}
	})

	t.Run("missing trade metadata", func(t *testing.T) {
		tx := bisqReleasedTransaction(t, "")
		tx.DexOfferCreation = nil
		h := &HealthChecker{bisq: &fakeBisqPositionSource{trade: &bisq.Trade{}}}
		if err := h.verifyBisqPositionReleased(context.Background(), tx); err == nil {
			t.Fatal("expected missing trade metadata error")
		}
	})
}

func TestVerifyBisqPositionReleasedAnomaliesAreDeduplicated(t *testing.T) {
	tests := []struct {
		name          string
		persistedTxID string
		trade         *bisq.Trade
	}{
		{
			name:  "trade failure",
			trade: &bisq.Trade{HasFailed: true, ErrorMessage: "payout failed"},
		},
		{
			name:          "payout txid mismatch",
			persistedTxID: "old-payout",
			trade:         &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "new-payout"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notifier := &fakeAnomalyNotifier{enabled: true}
			chain := &fakeBitcoinTransactionSource{}
			store := &fakeTransactionStore{}
			h := &HealthChecker{
				repo:    store,
				bisq:    &fakeBisqPositionSource{trade: test.trade},
				btc:     chain,
				anomaly: notifier,
			}
			tx := bisqReleasedTransaction(t, test.persistedTxID)
			for range 2 {
				if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
					t.Fatal(err)
				}
			}
			if len(notifier.calls) != 1 {
				t.Fatalf("anomaly calls = %d, want 1", len(notifier.calls))
			}
			if chain.calls != 0 || store.updates != 0 {
				t.Fatalf("chain calls=%d updates=%d", chain.calls, store.updates)
			}
		})
	}
}

func TestVerifyBisqPositionReleasedDoesNotConsumeDisabledAnomaly(t *testing.T) {
	notifier := &fakeAnomalyNotifier{}
	h := &HealthChecker{
		repo:    &fakeTransactionStore{},
		bisq:    &fakeBisqPositionSource{trade: &bisq.Trade{HasFailed: true, ErrorMessage: "payout failed"}},
		btc:     &fakeBitcoinTransactionSource{},
		anomaly: notifier,
	}
	tx := bisqReleasedTransaction(t, "")
	if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	notifier.enabled = true
	if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("anomaly calls = %d, want 1 after enabling", len(notifier.calls))
	}
}

func TestVerifyBisqPositionReleasedRetriesFailedAnomalyDelivery(t *testing.T) {
	notifier := &fakeAnomalyNotifier{enabled: true, err: errors.New("telegram unavailable")}
	h := &HealthChecker{
		repo:    &fakeTransactionStore{},
		bisq:    &fakeBisqPositionSource{trade: &bisq.Trade{HasFailed: true, ErrorMessage: "payout failed"}},
		btc:     &fakeBitcoinTransactionSource{},
		anomaly: notifier,
	}
	tx := bisqReleasedTransaction(t, "")
	if err := h.verifyBisqPositionReleased(context.Background(), tx); err == nil {
		t.Fatal("expected Telegram delivery error")
	}
	notifier.err = nil
	if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 2 {
		t.Fatalf("anomaly calls = %d, want failed delivery plus retry", len(notifier.calls))
	}
}

func TestVerifyBisqPositionReleasedRejectsDifferentChainTxID(t *testing.T) {
	notifier := &fakeAnomalyNotifier{enabled: true}
	store := &fakeTransactionStore{}
	h := &HealthChecker{
		repo:    store,
		bisq:    &fakeBisqPositionSource{trade: &bisq.Trade{IsPayoutPublished: true, PayoutTxID: "payout-1"}},
		btc:     &fakeBitcoinTransactionSource{status: &bitcoin.TxStatus{TxID: "other-payout", StatusLabel: "mempool"}},
		anomaly: notifier,
	}
	tx := bisqReleasedTransaction(t, "")
	if err := h.verifyBisqPositionReleased(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if tx.TransactionComplete || store.updates != 0 {
		t.Fatal("mismatched chain result must not complete the transaction")
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("anomaly calls = %d, want 1", len(notifier.calls))
	}
}

func TestVerifyDexPositionReleasedNoOpsForCompleteAndRoboSats(t *testing.T) {
	complete := bisqReleasedTransaction(t, "")
	complete.TransactionComplete = true
	source := &fakeBisqPositionSource{}
	h := &HealthChecker{bisq: source}
	if err := h.verifyBisqPositionReleased(context.Background(), complete); err != nil {
		t.Fatal(err)
	}
	if source.calls != 0 {
		t.Fatalf("Bisq calls for complete row = %d", source.calls)
	}

	robo := &db.Transaction{Dex: db.DexRoboSats, Status: db.StatusDexPositionReleased}
	if err := h.verifyDexPositionReleased(context.Background(), robo); err != nil {
		t.Fatal(err)
	}
	if robo.TransactionComplete {
		t.Fatal("RoboSats TODO path must not mark complete")
	}
}

func TestVerifyDexPositionReleasedWaitsForHedgelessPoolRestoration(t *testing.T) {
	tx := &db.Transaction{
		ID: 1, Dex: db.DexHodlHodl, Status: db.StatusDexPositionReleased,
		TradeMode: "hedgeless_double_collateral", BTCPoolRefillStatus: "awaiting_security_deposit",
	}
	h := &HealthChecker{}
	if err := h.verifyDexPositionReleased(context.Background(), tx); err != nil {
		t.Fatalf("verify released hedgeless trade: %v", err)
	}
	if tx.TransactionComplete {
		t.Fatal("transaction completed before the pool was restored")
	}
}
