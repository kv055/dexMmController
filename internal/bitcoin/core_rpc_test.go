package bitcoin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCoreRPCPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "rpc-user" || password != "rpc-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"jsonrpc":"1.0","id":"lnd-health","method":"ping"}` {
			t.Fatalf("request body = %s", body)
		}
		_, _ = w.Write([]byte(`{"result":null,"error":null,"id":"lnd-health"}`))
	}))
	defer server.Close()

	client := NewCoreRPCClient(server.URL, "rpc-user", "rpc-password")
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
