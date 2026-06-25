// Package hedgestream multiplexes per-venue live hedge-position feeds to any number
// of WebSocket subscribers. Each venue's upstream source (a native exchange WS or a
// REST poll) is started lazily on the first subscriber and stopped on the last
// unsubscribe; the latest snapshot is cached and replayed to every new subscriber.
package hedgestream

import (
	"context"
	"fmt"
	"sync"

	"dexMmController/internal/exchangeconnectors/cex"
)

// SourceFunc starts an upstream position feed for one venue and returns a channel of
// normalized updates. The source owns its own reconnect/backoff and must close the
// channel when ctx is cancelled.
type SourceFunc func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error)

// Manager fans out per-venue position updates to subscribers.
type Manager struct {
	mu      sync.Mutex
	sources map[string]SourceFunc
	venues  map[string]*venueState
}

type venueState struct {
	subscribers map[chan cex.HedgePositionUpdate]struct{}
	latest      *cex.HedgePositionUpdate
	cancel      context.CancelFunc
}

// NewManager creates a Manager backed by the given per-venue source factories.
func NewManager(sources map[string]SourceFunc) *Manager {
	return &Manager{
		sources: sources,
		venues:  make(map[string]*venueState),
	}
}

// Supported reports whether the manager has a source for venue.
func (m *Manager) Supported(venue string) bool {
	_, ok := m.sources[venue]
	return ok
}

// Subscribe registers a new subscriber for venue. It returns a receive channel, an
// unsubscribe function (idempotent; closes the channel), and an error if the venue is
// unknown or its source fails to start. The latest cached snapshot, if any, is
// delivered immediately on the returned channel.
func (m *Manager) Subscribe(venue string) (<-chan cex.HedgePositionUpdate, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[venue]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported hedge venue %q", venue)
	}

	vs := m.venues[venue]
	if vs == nil {
		vs = &venueState{subscribers: make(map[chan cex.HedgePositionUpdate]struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		vs.cancel = cancel
		ch, err := src(ctx)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		m.venues[venue] = vs
		go m.run(venue, vs, ch)
	}

	sub := make(chan cex.HedgePositionUpdate, 8)
	vs.subscribers[sub] = struct{}{}
	if vs.latest != nil {
		sub <- *vs.latest // buffer has room: a fresh channel never blocks here
	}

	unsub := func() { m.unsubscribe(venue, sub) }
	return sub, unsub, nil
}

// run pumps updates from a venue's source into its subscribers until the source
// channel closes (ctx cancelled). It exits early if the venue state has been replaced.
func (m *Manager) run(venue string, vs *venueState, ch <-chan cex.HedgePositionUpdate) {
	for update := range ch {
		m.mu.Lock()
		if m.venues[venue] != vs {
			m.mu.Unlock()
			return
		}
		u := update
		vs.latest = &u
		for sub := range vs.subscribers {
			select {
			case sub <- update:
			default: // drop if subscriber is too slow
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) unsubscribe(venue string, sub chan cex.HedgePositionUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vs := m.venues[venue]
	if vs == nil {
		return
	}
	if _, ok := vs.subscribers[sub]; !ok {
		return
	}
	delete(vs.subscribers, sub)
	close(sub)

	if len(vs.subscribers) == 0 {
		vs.cancel()
		delete(m.venues, venue)
	}
}
