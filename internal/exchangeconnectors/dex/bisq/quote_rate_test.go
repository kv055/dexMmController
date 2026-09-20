package bisq

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQuoteTurnIsPacedAndCancellable(t *testing.T) {
	client := &Client{}
	ctx := context.Background()
	if err := client.waitQuoteTurn(ctx); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := client.waitQuoteTurn(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second quote wasn't paced: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancelled quote waited for an entire rate-limit slot")
	}
}
