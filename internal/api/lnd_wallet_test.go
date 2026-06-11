package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lnd "dexMmController/internal/wallethub"
)

type fakeLNDWalletClient struct {
	nodeInfo *lnd.NodeInfoResponse
	channels []lnd.Channel
	peers    []lnd.Peer
	onChain  []lnd.WalletAddressProperty
	newAddr  string
}

func (f *fakeLNDWalletClient) NodeInfo(context.Context) (*lnd.NodeInfoResponse, error) {
	return f.nodeInfo, nil
}

func (f *fakeLNDWalletClient) ListChannels(context.Context) ([]lnd.Channel, error) {
	return f.channels, nil
}

func (f *fakeLNDWalletClient) ListPendingChannels(context.Context) (*lnd.PendingChannelsResponse, error) {
	return &lnd.PendingChannelsResponse{}, nil
}

func (f *fakeLNDWalletClient) ListPeers(context.Context) ([]lnd.Peer, error) {
	return f.peers, nil
}

func (f *fakeLNDWalletClient) ListWalletAddresses(context.Context) ([]lnd.WalletAddressProperty, error) {
	return f.onChain, nil
}

func (f *fakeLNDWalletClient) NewAddress(context.Context) (string, error) {
	return f.newAddr, nil
}

func (f *fakeLNDWalletClient) GraphNode(context.Context, string) (*lnd.GraphNode, error) {
	return &lnd.GraphNode{Node: struct {
		PubKey string `json:"pub_key"`
		Alias  string `json:"alias"`
		Color  string `json:"color"`
	}{Alias: "ACME Hub"}}, nil
}

func (f *fakeLNDWalletClient) ConnectPeer(context.Context, string, string) error {
	return nil
}

func (f *fakeLNDWalletClient) OpenChannel(context.Context, string, int64, int64, int64, bool) (*lnd.OpenChannelResponse, error) {
	return &lnd.OpenChannelResponse{FundingTxid: "abc123"}, nil
}

func (f *fakeLNDWalletClient) CloseChannel(context.Context, string, bool, int64) (*lnd.CloseChannelResponse, error) {
	return &lnd.CloseChannelResponse{ClosingTxid: "def456"}, nil
}

func TestHandleLNDWalletStatusRequiresClient(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletStatus(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleLNDWalletStatusReturnsChannels(t *testing.T) {
	client := &fakeLNDWalletClient{
		nodeInfo: &lnd.NodeInfoResponse{
			IdentityPubkey:    "03local",
			Alias:             "local-node",
			NumPeers:          1,
			NumActiveChannels: 1,
			SyncedToChain:     true,
			SyncedToGraph:     true,
		},
		channels: []lnd.Channel{{
			RemotePubkey: "03remote",
			Capacity:     "100000",
			LocalBalance: "90000",
			RemoteBalance: "10000",
			Active:       true,
			Private:      false,
			ChannelPoint: "abc:0",
		}},
		peers: []lnd.Peer{{
			PubKey:  "03remote",
			Address: "1.2.3.4:9735",
		}},
		onChain: []lnd.WalletAddressProperty{{
			Address: "bc1qexample",
			Balance: "5000",
		}},
	}
	s := (&Server{}).WithLNDWallet(client)
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet", nil)
	rec := httptest.NewRecorder()
	s.handleLNDWalletStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"remote_alias":"ACME Hub"`) {
		t.Fatalf("expected remote alias in response, got %s", body)
	}
	if !strings.Contains(body, `"kind":"lightning"`) {
		t.Fatalf("expected lightning identity in addresses, got %s", body)
	}
}

func TestHandleLNDOpenChannelRequiresPubkey(t *testing.T) {
	s := (&Server{}).WithLNDWallet(&fakeLNDWalletClient{})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/channels/open", strings.NewReader(`{"local_funding_amount_sat":1000}`))
	rec := httptest.NewRecorder()
	s.handleLNDOpenChannel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleLNDOpenChannelSuccess(t *testing.T) {
	s := (&Server{}).WithLNDWallet(&fakeLNDWalletClient{})
	body := `{"node_pubkey":"03peer","peer_host":"1.2.3.4:9735","local_funding_amount_sat":100000,"sat_per_vbyte":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/channels/open", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleLNDOpenChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleLNDCloseChannelSuccess(t *testing.T) {
	s := (&Server{}).WithLNDWallet(&fakeLNDWalletClient{})
	body := `{"channel_point":"abc:0","sat_per_vbyte":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/channels/close", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleLNDCloseChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestChannelHealth(t *testing.T) {
	if got := channelHealth(lnd.Channel{Active: false}); got != "inactive" {
		t.Fatalf("inactive channel health = %q", got)
	}
	if got := channelHealth(lnd.Channel{Active: true, Capacity: "100000", LocalBalance: "1000"}); got != "low_outbound" {
		t.Fatalf("low outbound health = %q", got)
	}
	if got := channelHealth(lnd.Channel{Active: true, Capacity: "100000", LocalBalance: "50000"}); got != "healthy" {
		t.Fatalf("healthy channel health = %q", got)
	}
}
