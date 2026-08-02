package openclaw

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// MockServer represents a mock server.
type MockServer struct {
	// Addr is the wallet or peer address used by the mock server.
	Addr string

	// For each code, treat it as "arrived" once time.Now() >= arriveAt.
	mu       sync.Mutex
	arriveAt map[string]time.Time

	srv *http.Server
	ln  net.Listener
}

// NewMockServer creates a mock server.
func NewMockServer(addr string) *MockServer {
	return &MockServer{
		Addr:     addr,
		arriveAt: make(map[string]time.Time),
	}
}

// SetArrivedAfter configures when a specific code becomes "arrived".
func (m *MockServer) SetArrivedAfter(code string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.arriveAt[code] = time.Now().Add(d)
}

// Start starts the configured worker.
func (m *MockServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", m.handleWS)

	ln, err := net.Listen("tcp", m.Addr)
	if err != nil {
		return err
	}
	m.ln = ln
	// If Addr used ":0", capture the actual bound address for callers.
	m.Addr = ln.Addr().String()

	m.srv = &http.Server{
		Handler: mux,
	}

	go func() {
		_ = m.srv.Serve(ln)
	}()
	return nil
}

// Close releases the underlying network resources.
func (m *MockServer) Close(ctx context.Context) error {
	if m.srv == nil {
		return nil
	}
	return m.srv.Shutdown(ctx)
}

func (m *MockServer) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // local mock
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "server close")

	for {
		var req reqMsg
		if err := wsjson.Read(r.Context(), c, &req); err != nil {
			return
		}

		if req.Type != "req" {
			continue
		}

		switch req.Method {
		case "bank.tx.arrived":
			var p BankTxArrivedParams
			_ = json.Unmarshal(req.Params, &p)

			arrived := false
			if p.Code != "" {
				m.mu.Lock()
				at := m.arriveAt[p.Code]
				m.mu.Unlock()
				if !at.IsZero() && !time.Now().Before(at) {
					arrived = true
				}
			}

			payload, _ := json.Marshal(BankTxArrivedPayload{Arrived: arrived})
			_ = wsjson.Write(r.Context(), c, resMsg{
				Type:    "res",
				ID:      req.ID,
				OK:      true,
				Payload: payload,
			})
		default:
			_ = wsjson.Write(r.Context(), c, resMsg{
				Type:  "res",
				ID:    req.ID,
				OK:    false,
				Error: &errMsg{Code: "unknown_method", Message: "unknown method"},
			})
		}
	}
}
