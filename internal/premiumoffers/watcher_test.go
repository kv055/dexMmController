package premiumoffers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// startWSServer starts an httptest server that records the X-Internal-Secret
// header from the WS handshake, accepts the upgrade, then closes immediately so
// the client's read fails and runSession returns without needing real DEX
// clients or a DB. Returns the ws:// URL and a channel yielding the seen secret.
func startWSServer(t *testing.T) (string, chan string) {
	t.Helper()
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("X-Internal-Secret")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.Close(websocket.StatusNormalClosure, "test done")
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), seen
}

// TestWatcherDialSendsInternalSecret asserts the watcher sends X-Internal-Secret
// on the WS handshake when a secret is configured.
func TestWatcherDialSendsInternalSecret(t *testing.T) {
	url, seen := startWSServer(t)
	w := NewWatcher(nil, nil, nil, nil, url, "s3cret")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = w.runSession(ctx) // returns once the server closes the connection

	select {
	case got := <-seen:
		if got != "s3cret" {
			t.Fatalf("X-Internal-Secret = %q, want %q", got, "s3cret")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the dial request")
	}
}

// TestWatcherDialOmitsSecretWhenEmpty asserts no secret header is sent when the
// watcher is configured without one (e.g. pointing at a browser session path).
func TestWatcherDialOmitsSecretWhenEmpty(t *testing.T) {
	url, seen := startWSServer(t)
	w := NewWatcher(nil, nil, nil, nil, url, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = w.runSession(ctx)

	select {
	case got := <-seen:
		if got != "" {
			t.Fatalf("X-Internal-Secret = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the dial request")
	}
}
