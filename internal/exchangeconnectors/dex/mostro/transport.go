package mostro

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// Transport establishes the response subscription before exposing Publish.
// Every operation owns its connections and must close them, including on timeout.
type Transport interface {
	Open(context.Context, nostr.Filter) (Connection, error)
	Query(context.Context, nostr.Filter) ([]nostr.Event, error)
}
type Connection interface {
	Events() <-chan nostr.Event
	// History contains the complete replay received before EOSE on every relay.
	History() []nostr.Event
	Publish(context.Context, nostr.Event) error
	Close()
}
type RelayTransport struct{ Relays []string }
type relayConnection struct {
	cancel  context.CancelFunc
	relays  []*nostr.Relay
	history []nostr.Event
	events  chan nostr.Event
	wg      sync.WaitGroup
	once    sync.Once
}

func (c *relayConnection) History() []nostr.Event     { return c.history }
func (c *relayConnection) Events() <-chan nostr.Event { return c.events }
func (c *relayConnection) Close() {
	c.once.Do(func() {
		c.cancel()
		for _, r := range c.relays {
			_ = r.Close()
		}
		c.wg.Wait()
	})
}
func (c *relayConnection) Publish(ctx context.Context, e nostr.Event) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	results := make(chan error, len(c.relays))
	for _, r := range c.relays {
		go func(r *nostr.Relay) { results <- r.Publish(ctx, e) }(r)
	}
	var errs []error
	for range c.relays {
		select {
		case err := <-results:
			if err == nil {
				return nil
			}
			errs = append(errs, err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("mostro: no relay acknowledged publication: %w", errors.Join(errs...))
}
func (t RelayTransport) Open(ctx context.Context, f nostr.Filter) (Connection, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &relayConnection{cancel: cancel, events: make(chan nostr.Event, 256)}
	replayCtx, stopReplay := context.WithTimeout(ctx, 12*time.Second)
	defer stopReplay()
	fail := func(err error) (Connection, error) {
		c.Close()
		return nil, fmt.Errorf("mostro: incomplete relay replay: %w", err)
	}
	for _, url := range t.Relays {
		r, err := nostr.RelayConnect(replayCtx, url)
		if err != nil {
			return fail(err)
		}
		sub, err := r.Subscribe(ctx, nostr.Filters{f})
		if err != nil {
			_ = r.Close()
			return fail(err)
		}
		c.relays = append(c.relays, r)
		count := 0
	replay:
		for {
			select {
			case e, ok := <-sub.Events:
				if !ok {
					sub.Unsub()
					return fail(fmt.Errorf("subscription closed before EOSE"))
				}
				count++
				// A full page does not establish complete history. Never act on
				// a possibly truncated replay; an operator can investigate it.
				if f.Limit > 0 && count >= f.Limit {
					sub.Unsub()
					return fail(fmt.Errorf("relay replay reached its limit"))
				}
				c.history = append(c.history, *e)
			case <-sub.EndOfStoredEvents:
				break replay
			case reason := <-sub.ClosedReason:
				sub.Unsub()
				return fail(fmt.Errorf("subscription closed: %s", reason))
			case <-replayCtx.Done():
				sub.Unsub()
				return fail(replayCtx.Err())
			}
		}

		// Subscribe synchronously writes REQ on this same connection before Publish.
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer sub.Unsub()
			for {
				select {
				case e, ok := <-sub.Events:
					if !ok {
						return
					}
					select {
					case c.events <- *e:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				case <-sub.ClosedReason:
					return
				}
			}
		}()
	}
	if len(c.relays) == 0 {
		c.Close()
		return nil, fmt.Errorf("mostro: no response subscription available")
	}
	go func() { c.wg.Wait(); close(c.events) }()
	return c, nil
}

// Query shares Open's all-relay EOSE barrier. A partial relay result cannot
// establish that an order is current or that a trade has no cancellation.
func (t RelayTransport) Query(ctx context.Context, f nostr.Filter) ([]nostr.Event, error) {
	c, err := t.Open(ctx, f)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var out []nostr.Event
	for _, e := range c.History() {
		if f.Matches(&e) && validEvent(e) {
			out = append(out, e)
		}
	}
	return out, nil
}
func validEvent(e nostr.Event) bool {
	if len(e.Content) > 65536 || !e.CheckID() {
		return false
	}
	ok, err := e.CheckSignature()
	return err == nil && ok
}
