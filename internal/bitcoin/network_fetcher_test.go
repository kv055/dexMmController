package bitcoin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTransactionMempool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tx/abc123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"txid":"abc123","status":{"confirmed":false,"block_height":0}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ndf := &NetworkDataFetcher{
		client:    srv.Client(),
		userAgent: "test",
		baseURL:   srv.URL,
	}
	status, err := ndf.FetchTransaction(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("FetchTransaction: %v", err)
	}
	if status.StatusLabel != "mempool" || status.Confirmed {
		t.Fatalf("status: %+v", status)
	}
}

func TestFetchTransactionConfirmed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tx/confirmed1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"txid":"confirmed1","status":{"confirmed":true,"block_height":100}}`))
	})
	mux.HandleFunc("/api/blocks/tip/height", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`102`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ndf := &NetworkDataFetcher{
		client:    srv.Client(),
		userAgent: "test",
		baseURL:   srv.URL,
	}
	status, err := ndf.FetchTransaction(context.Background(), "confirmed1")
	if err != nil {
		t.Fatalf("FetchTransaction: %v", err)
	}
	if status.StatusLabel != "confirmed" || status.Confirmations != 3 {
		t.Fatalf("status: %+v", status)
	}
}

func TestFetchTransactionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ndf := &NetworkDataFetcher{
		client:    srv.Client(),
		userAgent: "test",
		baseURL:   srv.URL,
	}
	status, err := ndf.FetchTransaction(context.Background(), "missing")
	if err != nil {
		t.Fatalf("FetchTransaction: %v", err)
	}
	if status.StatusLabel != "not_found" {
		t.Fatalf("status: %+v", status)
	}
}
