package hedgestream

import (
	"context"
	"sync"
	"testing"
	"time"

	"dexMmController/internal/exchanges/cex"
)

// fakeSource is a controllable SourceFunc for exercising the manager lifecycle.
type fakeSource struct {
	mu       sync.Mutex
	starts   int
	stops    int
	feed     chan cex.HedgePositionUpdate
	stopped  chan struct{}
	cancelFn context.CancelFunc
}

func newFakeSource() *fakeSource {
	return &fakeSource{feed: make(chan cex.HedgePositionUpdate, 8), stopped: make(chan struct{}, 4)}
}

func (f *fakeSource) fn(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()

	innerCtx, cancel := context.WithCancel(ctx)
	f.cancelFn = cancel
	out := make(chan cex.HedgePositionUpdate)
	go func() {
		defer close(out)
		for {
			select {
			case <-innerCtx.Done():
				f.mu.Lock()
				f.stops++
				f.mu.Unlock()
				f.stopped <- struct{}{}
				return
			case u := <-f.feed:
				select {
				case out <- u:
				case <-innerCtx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (f *fakeSource) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

func recvWithTimeout(t *testing.T, ch <-chan cex.HedgePositionUpdate) cex.HedgePositionUpdate {
	t.Helper()
	select {
	case u := <-ch:
		return u
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for update")
		return cex.HedgePositionUpdate{}
	}
}

func TestManagerUnsupportedVenue(t *testing.T) {
	m := NewManager(map[string]SourceFunc{})
	if _, _, err := m.Subscribe("nope"); err == nil {
		t.Error("expected error for unsupported venue")
	}
	if m.Supported("nope") {
		t.Error("Supported should be false")
	}
}

func TestManagerLazyStartFanoutAndStop(t *testing.T) {
	fs := newFakeSource()
	m := NewManager(map[string]SourceFunc{"kraken_futures": fs.fn})

	// First subscriber starts the source.
	sub1, unsub1, err := m.Subscribe("kraken_futures")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := fs.startCount(); got != 1 {
		t.Fatalf("expected source started once, got %d", got)
	}

	// An update fans out to the subscriber and is cached as latest.
	fs.feed <- cex.HedgePositionUpdate{Venue: "kraken_futures", MarkPrice: 50000}
	if u := recvWithTimeout(t, sub1); u.MarkPrice != 50000 {
		t.Errorf("sub1 mark: got %v", u.MarkPrice)
	}

	// Second subscriber does NOT restart the source and immediately gets the cached snapshot.
	sub2, unsub2, err := m.Subscribe("kraken_futures")
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	if got := fs.startCount(); got != 1 {
		t.Fatalf("expected source still started once, got %d", got)
	}
	if u := recvWithTimeout(t, sub2); u.MarkPrice != 50000 {
		t.Errorf("sub2 cached snapshot mark: got %v", u.MarkPrice)
	}

	// A new update reaches both subscribers.
	fs.feed <- cex.HedgePositionUpdate{Venue: "kraken_futures", MarkPrice: 51000}
	if u := recvWithTimeout(t, sub1); u.MarkPrice != 51000 {
		t.Errorf("sub1 second mark: got %v", u.MarkPrice)
	}
	if u := recvWithTimeout(t, sub2); u.MarkPrice != 51000 {
		t.Errorf("sub2 second mark: got %v", u.MarkPrice)
	}

	// Unsubscribing the first does NOT stop the source (sub2 still active).
	unsub1()
	unsub1() // idempotent
	select {
	case <-fs.stopped:
		t.Fatal("source stopped while a subscriber remained")
	case <-time.After(150 * time.Millisecond):
	}

	// Last unsubscribe stops the source (ctx cancelled).
	unsub2()
	select {
	case <-fs.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("source was not stopped after last unsubscribe")
	}
}

func TestManagerDropsSlowSubscriber(t *testing.T) {
	fs := newFakeSource()
	m := NewManager(map[string]SourceFunc{"kraken_futures": fs.fn})

	// Slow subscriber: never drained.
	slow, unsubSlow, err := m.Subscribe("kraken_futures")
	if err != nil {
		t.Fatalf("subscribe slow: %v", err)
	}
	_ = slow
	defer unsubSlow()

	// Fast subscriber.
	fast, unsubFast, err := m.Subscribe("kraken_futures")
	if err != nil {
		t.Fatalf("subscribe fast: %v", err)
	}
	defer unsubFast()
	// Drain the cached snapshot replay (none yet) — fast has no buffered item.

	// Push many updates; the manager must not block on the slow subscriber's full buffer.
	for i := 0; i < 50; i++ {
		fs.feed <- cex.HedgePositionUpdate{Venue: "kraken_futures", MarkPrice: float64(i)}
	}
	// Fast subscriber keeps receiving despite the slow one being stuck.
	got := recvWithTimeout(t, fast)
	if got.Venue != "kraken_futures" {
		t.Errorf("fast subscriber got unexpected update: %+v", got)
	}
}
