package magma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetInfoReturnsSyncStatusWhenChainNotReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Info{
			MinInitialLSPBalanceSat: "500000",
			MaxInitialLSPBalanceSat: "100000000",
		})
	}))
	defer server.Close()

	coordinator := &Coordinator{
		Client: NewClient(server.URL + "/api/lsp/v1"),
		Wallet: func(context.Context) LNDWallet {
			return fakeWallet{pubkey: "03local", syncedToChain: false, blockHeight: 963039}
		},
	}

	info, err := coordinator.GetInfo(context.Background(), PoolRecommended)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if info.ChainReady || info.SyncedToChain {
		t.Fatalf("expected chain not ready, got %+v", info)
	}
	if info.BlockHeight != 963039 || info.PublicKey != "03local" {
		t.Fatalf("unexpected summary: %+v", info)
	}
}
