package tradeflow

import (
	"context"
	"dexTakerController/internal/db"
	"encoding/json"
	"errors"
)

var ErrBisqWaiting = errors.New("Bisq action is awaiting external confirmation")

// BisqStep has no side effects until Perform. Reconcile must be read-only and
// must never infer that an absent receipt authorizes a fresh financial request.
type BisqStep struct {
	Kind      string
	Request   json.RawMessage
	Perform   func(context.Context, string) (json.RawMessage, error)
	Reconcile func(context.Context, db.BisqAction) (json.RawMessage, error)
}

// BisqStepDriver is explicitly wired only when all bounded venue adapters are
// available. There is deliberately no fallback to synchronous TradeFlow.Run.
type BisqStepDriver interface {
	Ready(context.Context, Order) error
	Next(context.Context, Order, []db.BisqAction) (*BisqStep, error)
}

func (tf *TradeFlow) WithBisqStepDriver(driver BisqStepDriver) *TradeFlow {
	tf.bisqDriver = driver
	return tf
}
func (tf *TradeFlow) BisqReady(ctx context.Context, order Order) error {
	if tf.bisqDriver == nil {
		return errors.New("bounded Bisq execution adapters are not configured")
	}
	if err := validate(order); err != nil {
		return err
	}
	if err := tf.requireCapabilities(ctx, order); err != nil {
		return err
	}
	return tf.bisqDriver.Ready(ctx, order)
}
func (tf *TradeFlow) NextBisqStep(ctx context.Context, order Order, actions []db.BisqAction) (*BisqStep, error) {
	if tf.bisqDriver == nil {
		return nil, errors.New("bounded Bisq execution adapters are not configured")
	}
	return tf.bisqDriver.Next(ctx, order, actions)
}
