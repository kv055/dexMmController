package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lnd "dexMmController/internal/wallethub"
)

type fakeLNDWalletClient struct {
	nodeInfo *lnd.NodeInfoResponse
	channels []lnd.Channel
	peers    []lnd.Peer
	onChain  []lnd.WalletAddressProperty
	newAddr  string
}

type blockingLNDWalletClient struct{ fakeLNDWalletClient }

func waitForCancellation[T any](ctx context.Context) (T, error) {
	var zero T
	<-ctx.Done()
	return zero, ctx.Err()
}

func (b *blockingLNDWalletClient) NodeInfo(ctx context.Context) (*lnd.NodeInfoResponse, error) {
	return waitForCancellation[*lnd.NodeInfoResponse](ctx)
}
func (b *blockingLNDWalletClient) ListChannels(ctx context.Context) ([]lnd.Channel, error) {
	return waitForCancellation[[]lnd.Channel](ctx)
}
func (b *blockingLNDWalletClient) ListPendingChannels(ctx context.Context) (*lnd.PendingChannelsResponse, error) {
	return waitForCancellation[*lnd.PendingChannelsResponse](ctx)
}
func (b *blockingLNDWalletClient) ListPeers(ctx context.Context) ([]lnd.Peer, error) {
	return waitForCancellation[[]lnd.Peer](ctx)
}
func (b *blockingLNDWalletClient) ListWalletAddresses(ctx context.Context) ([]lnd.WalletAddressProperty, error) {
	return waitForCancellation[[]lnd.WalletAddressProperty](ctx)
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

func (f *fakeLNDWalletClient) OpenChannel(context.Context, string, int64, int64, int64, int64, bool) (*lnd.OpenChannelResponse, error) {
	return &lnd.OpenChannelResponse{FundingTxid: "abc123", OutputIndex: 0}, nil
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
			RemotePubkey:  "03remote",
			Capacity:      "100000",
			LocalBalance:  "90000",
			RemoteBalance: "10000",
			Active:        true,
			Private:       false,
			ChannelPoint:  "abc:0",
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

func TestHandleLNDWalletStatusReturnsDegradedResponseBeforeCallerDeadline(t *testing.T) {
	s := (&Server{}).WithLNDWallet(&blockingLNDWalletClient{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/wallet", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	started := time.Now()
	s.handleLNDWalletStatus(rec, req)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("handler took %s", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"degraded":true`) || !strings.Contains(body, `"warnings":[`) {
		t.Fatalf("expected degraded partial response, got %s", body)
	}
}

func TestHandleServiceVersionsDoesNotBlockOnDegradedLND(t *testing.T) {
	s := (&Server{}).
		WithServiceVersions(func(context.Context) ServiceVersions {
			return ServiceVersions{Bisq1: "1.10.6", Bisq2: "2.1.11", LND: "fallback"}
		}).
		WithLNDWallet(&blockingLNDWalletClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/service-versions", nil)
	rec := httptest.NewRecorder()

	started := time.Now()
	s.handleServiceVersions(rec, req)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("handler took %s", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"lnd":"fallback"`) {
		t.Fatalf("expected fallback version in response, got %s", body)
	}
}

func TestShouldDisplayWalletAddress(t *testing.T) {
	primary := "bc1qprimary"
	for _, tc := range []struct {
		name    string
		address string
		balance int64
		want    bool
	}{
		{name: "primary with no balance", address: primary, want: true},
		{name: "funded derived address", address: "bc1qfunded", balance: 1, want: true},
		{name: "unused derived address", address: "bc1qunused", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDisplayWalletAddress(tc.address, primary, tc.balance); got != tc.want {
				t.Fatalf("shouldDisplayWalletAddress(%q, %q, %d) = %t, want %t", tc.address, primary, tc.balance, got, tc.want)
			}
		})
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

func TestHandleLNDChannelPresets(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/lnd/channel-presets", nil)
	rec := httptest.NewRecorder()
	s.handleLNDChannelPresets(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"kraken"`) {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleLNDOpenChannelPresetEnforcesMinimum(t *testing.T) {
	s := (&Server{}).WithLNDWallet(&fakeLNDWalletClient{})
	req := httptest.NewRequest(http.MethodPost, "/api/lnd/channels/open", strings.NewReader(`{"preset_id":"kraken","local_funding_amount_sat":100000}`))
	rec := httptest.NewRecorder()
	s.handleLNDOpenChannel(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "requires at least") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
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
