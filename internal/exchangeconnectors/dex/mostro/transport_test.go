package mostro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"nhooyr.io/websocket"
)

func replayRelay(t *testing.T, history []nostr.Event, gate <-chan struct{}, requested chan<- struct{}, closed bool) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, raw, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req []json.RawMessage
		if json.Unmarshal(raw, &req) != nil || len(req) < 2 {
			return
		}
		var id string
		if json.Unmarshal(req[1], &id) != nil {
			return
		}
		if requested != nil {
			requested <- struct{}{}
		}
		send := func(v any) bool { b, _ := json.Marshal(v); return c.Write(ctx, websocket.MessageText, b) == nil }
		for _, e := range history {
			if !send([]any{"EVENT", id, e}) {
				return
			}
		}
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return
			}
		}
		if closed {
			send([]any{"CLOSED", id, "history unavailable"})
			return
		}
		if !send([]any{"EOSE", id}) {
			return
		}
		// Keep the connection alive until the subscriber closes it.
		_, _, _ = c.Read(ctx)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}
func TestRelayOpenRequiresEOSEFromEveryRelay(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	e := nostr.Event{Kind: 14, CreatedAt: nostr.Now(), Tags: nostr.Tags{}, Content: "history"}
	if err := e.Sign(key); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	seen := make(chan struct{}, 1)
	first := replayRelay(t, []nostr.Event{e}, nil, nil, false)
	second := replayRelay(t, []nostr.Event{e}, gate, seen, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type result struct {
		c   Connection
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := (RelayTransport{Relays: []string{first, second}}).Open(ctx, nostr.Filter{Kinds: []int{14}, Limit: 100})
		done <- result{c, err}
	}()
	select {
	case <-seen:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case r := <-done:
		if r.c != nil {
			r.c.Close()
		}
		t.Fatal("Open returned before second EOSE")
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		defer r.c.Close()
		if len(r.c.History()) != 2 {
			t.Fatalf("history=%d", len(r.c.History()))
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestRelayQueryRejectsPartialOrTruncatedHistory(t *testing.T) {
	for _, mode := range []string{"closed", "timeout", "limit"} {
		t.Run(mode, func(t *testing.T) {
			key := nostr.GeneratePrivateKey()
			e := nostr.Event{Kind: 14, CreatedAt: nostr.Now(), Content: "history"}
			if err := e.Sign(key); err != nil {
				t.Fatal(err)
			}
			first := replayRelay(t, nil, nil, nil, false)
			var gate chan struct{}
			if mode == "timeout" {
				gate = make(chan struct{})
				defer close(gate)
			}
			second := replayRelay(t, []nostr.Event{e}, gate, nil, mode == "closed")
			limit := 100
			if mode == "limit" {
				limit = 1
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			if _, err := (RelayTransport{Relays: []string{first, second}}).Query(ctx, nostr.Filter{Kinds: []int{14}, Limit: limit}); err == nil {
				t.Fatal("incomplete history accepted")
			}
		})
	}
}
