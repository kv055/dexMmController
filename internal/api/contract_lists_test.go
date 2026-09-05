package api

import (
	lnd "dexMmController/internal/wallethub"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAPILSPCandidatesResponse(t *testing.T) {
	for _, channels := range [][]lnd.Channel{nil, {{ChannelPoint: "fixture:0", RemotePubkey: "fixture-peer", Capacity: "100000", Active: true}}} {
		s := (&Server{}).WithLNDWallet(&fakeLNDWalletClient{channels: channels})
		mux := http.NewServeMux()
		s.registerRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/lnd/lsp-channel/candidates", nil))
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var items []json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil || items == nil || len(items) != len(channels) {
			t.Fatalf("expected %d candidates: %s (%v)", len(channels), rec.Body.String(), err)
		}
	}
}
