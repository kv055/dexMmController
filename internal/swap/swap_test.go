package swap

import (
	"strings"
	"testing"
	"time"
)

func TestQuoteRequiresBothFeeCeilingsAndHonoursBoth(t *testing.T) {
	q := Quote{LightningOutputSat: 100000, LockupAmountSat: 100500, ServiceFeeSat: 200, MinerFeeSat: 100, ExpiresAt: time.Now().Add(time.Minute)}
	if err := q.Validate(time.Now(), 0, 10000); err == nil {
		t.Fatal("missing absolute limit accepted")
	}
	if err := q.Validate(time.Now(), 500, 1000); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("relative ceiling: %v", err)
	}
	if err := q.Validate(time.Now(), 250, 10000); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("absolute ceiling: %v", err)
	}
	if err := q.Validate(time.Now(), 500, 5000); err != nil {
		t.Fatal(err)
	}
}
