package bisqexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"dexTakerController/internal/db"
	"dexTakerController/internal/tradeflow"
	"github.com/google/uuid"
)

type DurableStore interface {
	AcceptBisqQuote(context.Context, string, string, func(*db.BisqExecutionQuote, time.Time) (*db.Transaction, error)) (*db.Transaction, error)
	ClaimBisqRun(context.Context, uint, string) (*db.BisqLease, error)
	RenewBisqLease(context.Context, db.BisqLease) error
	ReleaseBisqLease(context.Context, db.BisqLease) error
	BeginBisqAction(context.Context, db.BisqLease, db.BisqAction) (bool, error)
	CompleteBisqAction(context.Context, db.BisqLease, string, json.RawMessage) error
	GetBisqRun(context.Context, uint) (*db.BisqExecutionRun, []db.BisqAction, error)
	ListBisqRuns(context.Context) ([]db.BisqExecutionRun, error)
	ParkBisqRun(context.Context, db.BisqLease, string) error
	FinishBisqRun(context.Context, db.BisqLease) error
}

type StepRunner interface {
	BisqReady(context.Context, tradeflow.Order) error
	NextBisqStep(context.Context, tradeflow.Order, []db.BisqAction) (*tradeflow.BisqStep, error)
}

func acceptedRow(q *db.BisqExecutionQuote, key string, now time.Time) (*db.Transaction, error) {
	plan, order, err := planFromQuote(q)
	if err != nil {
		return nil, err
	}
	plan.Stage = db.BisqStageAccepted
	plan.DeadlineAt = now.Add(acceptedDeadline)
	plan.IdempotencyKey = key
	order.BisqPlan = plan
	snapshot, err := json.Marshal(order)
	if err != nil {
		return nil, err
	}
	meta := db.DexOfferMeta{SelectedOffer: q.SelectedOffer, ResolvedRoute: order.ResolvedRoute, TradeflowAt: &now, TradeflowOrder: snapshot, BisqExecution: plan}
	raw, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return nil, err
	}
	status := db.StatusCexTradeflowStarted
	if q.TradeMode == "hedgeless_double_collateral" {
		status = db.StatusHedgelessTradeStarted
	}
	return &db.Transaction{Direction: "bid", FiatAmount: plan.FiatAmount, FiatCurrency: order.FiatCurrency, PaymentMethod: q.PaymentMethod,
		Cex: db.CexExchangeFromProvider(order.Provider), Dex: db.DexBisq, Status: status, DexOfferID: q.OfferID, TradeMode: q.TradeMode,
		BTCRequiredLiquidity: order.RequiredBTCLiquidity, DestinationAddress: plan.FundingAddress, DexOfferCreation: raw}, nil
}

func (s *Service) executeDurable(parent context.Context, id uint) {
	store, ok := s.store.(DurableStore)
	driver, ready := s.runner.(StepRunner)
	if !s.enabled || !ok || !ready || id == 0 {
		return
	}
	run, _, err := store.GetBisqRun(parent, id)
	if err != nil || run == nil || run.Version != db.BisqExecutionVersion || run.Stage == db.BisqStageTaken || run.Stage == db.BisqStageReviewRequired {
		return
	}
	lease, err := store.ClaimBisqRun(parent, id, uuid.NewString())
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.RenewBisqLease(ctx, *lease); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-heartbeatDone
		cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer stop()
		_ = store.ReleaseBisqLease(cleanup, *lease)
	}()
	park := func(reason string) {
		if err := store.ParkBisqRun(ctx, *lease, reason); err != nil {
			slog.Error("bisq: park execution", "transaction_id", id, "error", err)
		}
	}
	tx, err := s.store.GetTransaction(id)
	if err != nil {
		return
	}
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil || meta.BisqExecution == nil || meta.BisqExecution.Version != 2 {
		park("legacy or unreadable execution; explicit reconciliation required")
		return
	}
	var order tradeflow.Order
	if err = json.Unmarshal(meta.TradeflowOrder, &order); err != nil {
		park("unreadable order snapshot")
		return
	}
	order.TransactionID = id
	order.BisqPlan = meta.BisqExecution
	// The database deadline is authoritative, not a stale JSON snapshot.
	order.BisqPlan.DeadlineAt = run.DeadlineAt
	// A bounded burst advances ready steps; waiting does not hold a goroutine or
	// HTTP request. The next tick resumes from the action ledger.
	for n := 0; n < 16; n++ {
		_, actions, err := store.GetBisqRun(ctx, id)
		if err != nil {
			return
		}
		step, err := driver.NextBisqStep(ctx, order, actions)
		if errors.Is(err, tradeflow.ErrBisqWaiting) {
			return
		}
		if err != nil {
			park(err.Error())
			return
		}
		if step == nil {
			if err := store.FinishBisqRun(ctx, *lease); err != nil {
				slog.Error("bisq: finish", "error", err)
			}
			return
		}
		if step.Kind == "" || step.Perform == nil || !json.Valid(step.Request) || string(step.Request) == "null" {
			park("invalid Bisq action definition")
			return
		}
		var previous *db.BisqAction
		for i := range actions {
			if actions[i].Kind == step.Kind {
				previous = &actions[i]
				break
			}
		}
		if previous != nil {
			if !sameRequest(previous.Request, step.Request) {
				park("immutable action request changed: " + step.Kind)
				return
			}
			if previous.State != "intent" {
				park("invalid action transition: " + step.Kind)
				return
			}
			// Reconcile even after the spending deadline; never Perform an old intent.
			if step.Reconcile == nil {
				park("ambiguous " + step.Kind + "; no safe reconciliation available")
				return
			}
			receipt, err := step.Reconcile(ctx, *previous)
			if errors.Is(err, tradeflow.ErrBisqWaiting) {
				return
			}
			if err != nil {
				park("reconcile " + step.Kind + ": " + err.Error())
				return
			}
			if err = store.CompleteBisqAction(ctx, *lease, step.Kind, receipt); err != nil {
				return
			}
			continue
		}
		if !s.now().Before(run.DeadlineAt) {
			park("accepted Bisq execution deadline expired")
			return
		}
		if err := store.RenewBisqLease(ctx, *lease); err != nil {
			return
		}
		action := db.BisqAction{TransactionID: id, Kind: step.Kind, ExternalKey: fmt.Sprintf("bisq-%d-%s", id, step.Kind), Request: step.Request}
		created, err := store.BeginBisqAction(ctx, *lease, action)
		if errors.Is(err, db.ErrBisqExpired) {
			park(err.Error())
			return
		}
		if err != nil || !created {
			return
		}
		if ctx.Err() != nil {
			return
		}
		// Adapters also enforce the deadline at the venue boundary. Cancellation
		// alone cannot undo an external request whose response was lost.
		actionCtx, stop := context.WithDeadline(ctx, run.DeadlineAt)
		receipt, err := step.Perform(actionCtx, action.ExternalKey)
		stop()
		if err != nil {
			// The persisted intent is deliberately retained. A later worker must
			// reconcile the request, even when this error looks like a transport failure.
			slog.Warn("bisq: action needs reconciliation", "transaction_id", id, "kind", step.Kind, "error", err)
			return
		}
		if err = store.CompleteBisqAction(ctx, *lease, step.Kind, receipt); err != nil {
			return
		}
	}
}

func sameRequest(a, b json.RawMessage) bool {
	var left, right any
	// UseNumber avoids equating distinct integer amounts above 2^53.
	decode := func(raw json.RawMessage, dst *any) error {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		return decoder.Decode(dst)
	}
	return decode(a, &left) == nil && decode(b, &right) == nil && reflect.DeepEqual(left, right)
}
