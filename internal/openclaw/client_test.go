package openclaw

import (
	"context"
	"testing"
	"time"
)

func TestPollBankTxArrived(t *testing.T) {
	mock := NewMockServer("127.0.0.1:0")
	mock.SetArrivedAfter("xyz123", 200*time.Millisecond)
	if err := mock.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = mock.Close(context.Background()) })

	// Discover the actual port.
	addr := mock.ln.Addr().String()

	c := NewClient("ws://" + addr + "/ws")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	arrived, err := c.PollBankTxArrivedFromDex(ctx, "xyz123", 25*time.Millisecond)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !arrived {
		t.Fatalf("expected arrived=true")
	}
}

