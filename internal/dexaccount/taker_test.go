package dexaccount

import (
	"context"
	"errors"
	"testing"
)

func TestResolveDepositBisqIsImmediateNoOp(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	address, invoice, err := taker.ResolveDeposit(context.Background(), "bisq", map[string]any{"exchange": "bisq"})
	if err != nil {
		t.Fatalf("ResolveDeposit: %v", err)
	}
	if address != "" || invoice != "" {
		t.Fatalf("Bisq destination = (%q, %q), want empty direct-route result", address, invoice)
	}
}

func TestResolveDepositStillRejectsNilExecution(t *testing.T) {
	taker := NewTaker(nil, nil, nil, nil, nil)
	if _, _, err := taker.ResolveDeposit(context.Background(), "bisq", nil); err == nil {
		t.Fatal("expected nil execution payload error")
	}
}

func TestResolveDepositRoboSatsHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	taker := NewTaker(nil, nil, nil, nil, nil)
	_, _, err := taker.ResolveDeposit(ctx, "robosats", map[string]any{"position_id": "position-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveDeposit error = %v, want context.Canceled", err)
	}
}
