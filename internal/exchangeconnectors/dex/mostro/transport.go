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
	Publish(context.Context, nostr.Event) error
	Close()
}
type RelayTransport struct{ Relays []string }
type relayConnection struct {
	cancel context.CancelFunc
	relays []*nostr.Relay
	events chan nostr.Event
	wg     sync.WaitGroup
	once   sync.Once
}

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
	for _, url := range t.Relays {
		dial, cancelDial := context.WithTimeout(ctx, 5*time.Second)
		r, err := nostr.RelayConnect(dial, url)
		cancelDial()
		if err != nil {
			continue
		}
		sub, err := r.Subscribe(ctx, nostr.Filters{f})
		if err != nil {
			_ = r.Close()
			continue
		}
		// Subscribe synchronously writes REQ on this same connection before Publish.
		c.relays = append(c.relays, r)
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
func (t RelayTransport) Query(ctx context.Context, f nostr.Filter) ([]nostr.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var out []nostr.Event
	var errs []error
	complete := false
	// Query each configured relay. EOSE is required: a timeout cannot establish
	// that a selected replaceable order is still the newest event.
	for _, url := range t.Relays {
		r, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sub, err := r.Subscribe(ctx, nostr.Filters{f})
		if err != nil {
			_ = r.Close()
			errs = append(errs, err)
			continue
		}
	loop:
		for {
			select {
			case e, ok := <-sub.Events:
				if !ok {
					break loop
				}
				if f.Matches(e) && validEvent(*e) {
					out = append(out, *e)
				}
			case <-sub.EndOfStoredEvents:
				complete = true
				break loop
			case <-sub.ClosedReason:
				break loop
			case <-ctx.Done():
				break loop
			}
		}
		sub.Unsub()
		_ = r.Close()
	}
	if !complete {
		return nil, fmt.Errorf("mostro: incomplete relay query: %w", errors.Join(errs...))
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
