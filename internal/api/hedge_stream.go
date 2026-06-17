package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"dexMmController/internal/hedgestream"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// WithHedgeStream attaches the live hedge-position streaming manager to the server.
func (s *Server) WithHedgeStream(manager *hedgestream.Manager) *Server {
	s.hedgeStream = manager
	return s
}

// handleCexHedgeStream upgrades the request to a WebSocket and relays the live,
// normalized hedge position for ?venue= (kraken_futures|binance_futures|
// coinbase_futures) until the client disconnects. The caller is dexMonitor's
// server-side relay (not a browser), so origin checks are skipped.
func (s *Server) handleCexHedgeStream(w http.ResponseWriter, r *http.Request) {
	if s.hedgeStream == nil {
		http.Error(w, "hedge stream is not configured", http.StatusServiceUnavailable)
		return
	}
	venue := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("venue")))
	if venue == "" {
		http.Error(w, "venue query parameter is required", http.StatusBadRequest)
		return
	}
	if !s.hedgeStream.Supported(venue) {
		http.Error(w, "unsupported hedge venue; supported: kraken_futures, binance_futures, coinbase_futures", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "stream closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub, unsub, err := s.hedgeStream.Subscribe(venue)
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer unsub()

	// Drain client control frames; any read error means the client is gone.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-sub:
			if !ok {
				return
			}
			writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(writeCtx, conn, update)
			writeCancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}
