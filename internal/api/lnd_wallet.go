package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

type lndWalletClient interface {
	NodeInfo(ctx context.Context) (*lnd.NodeInfoResponse, error)
	ListChannels(ctx context.Context) ([]lnd.Channel, error)
	ListPendingChannels(ctx context.Context) (*lnd.PendingChannelsResponse, error)
	ListPeers(ctx context.Context) ([]lnd.Peer, error)
	ListWalletAddresses(ctx context.Context) ([]lnd.WalletAddressProperty, error)
	NewAddress(ctx context.Context) (string, error)
	GraphNode(ctx context.Context, pubKey string) (*lnd.GraphNode, error)
	ConnectPeer(ctx context.Context, pubkey, host string) error
	OpenChannel(
		ctx context.Context,
		nodePubkey string,
		localFundingAmount, pushSat, satPerVbyte int64,
		private bool,
	) (*lnd.OpenChannelResponse, error)
	CloseChannel(
		ctx context.Context,
		channelPoint string,
		force bool,
		satPerVbyte int64,
	) (*lnd.CloseChannelResponse, error)
}

type lndNodeSummary struct {
	IdentityPubkey    string `json:"identity_pubkey"`
	Alias             string `json:"alias"`
	NumPeers          uint32 `json:"num_peers"`
	NumActiveChannels uint32 `json:"num_active_channels"`
	SyncedToChain     bool   `json:"synced_to_chain"`
	SyncedToGraph     bool   `json:"synced_to_graph"`
	BlockHeight       uint32 `json:"block_height"`
}

type lndPeerSummary struct {
	PubKey   string `json:"pub_key"`
	Address  string `json:"address"`
	Inbound  bool   `json:"inbound"`
	SyncType string `json:"sync_type"`
}

type lndChannelSummary struct {
	RemotePubkey      string `json:"remote_pubkey"`
	RemoteAlias       string `json:"remote_alias"`
	PeerAddress       string `json:"peer_address"`
	CapacitySat       int64  `json:"capacity_sat"`
	LocalBalanceSat   int64  `json:"local_balance_sat"`
	RemoteBalanceSat  int64  `json:"remote_balance_sat"`
	Active            bool   `json:"active"`
	Private           bool   `json:"private"`
	Health            string `json:"health"`
	ChannelPoint      string `json:"channel_point"`
	TotalSentSat      int64  `json:"total_sent_sat"`
	TotalReceivedSat  int64  `json:"total_received_sat"`
}

type lndPendingChannelSummary struct {
	RemotePubkey string `json:"remote_pubkey"`
	RemoteAlias  string `json:"remote_alias"`
	CapacitySat  int64  `json:"capacity_sat"`
	LocalBalance int64  `json:"local_balance_sat"`
	RemoteBalance int64 `json:"remote_balance_sat"`
	State        string `json:"state"`
}

type lndWalletAddressEntry struct {
	Address     string `json:"address"`
	Kind        string `json:"kind"`
	Label       string `json:"label,omitempty"`
	BalanceSat  int64  `json:"balance_sat"`
	IsInternal  bool   `json:"is_internal,omitempty"`
}

type lndWalletStatusResponse struct {
	Node             lndNodeSummary             `json:"node"`
	Peers            []lndPeerSummary           `json:"peers"`
	Channels         []lndChannelSummary        `json:"channels"`
	PendingChannels  []lndPendingChannelSummary `json:"pending_channels"`
	Addresses        []lndWalletAddressEntry    `json:"addresses"`
	CheckedAt        string                     `json:"checked_at"`
}

type createLNDAddressRequest struct {
	Label string `json:"label"`
}

type createLNDAddressResponse struct {
	Address   string `json:"address"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
}

type openLNDChannelRequest struct {
	NodePubkey            string `json:"node_pubkey"`
	PeerHost              string `json:"peer_host"`
	LocalFundingAmountSat int64  `json:"local_funding_amount_sat"`
	PushSat               int64  `json:"push_sat"`
	SatPerVbyte           int64  `json:"sat_per_vbyte"`
	Private               bool   `json:"private"`
}

type openLNDChannelResponse struct {
	FundingTxid string `json:"funding_txid"`
}

type closeLNDChannelRequest struct {
	ChannelPoint string `json:"channel_point"`
	Force        bool   `json:"force"`
	SatPerVbyte  int64  `json:"sat_per_vbyte"`
}

type closeLNDChannelResponse struct {
	ClosingTxid string `json:"closing_txid"`
}

// WithLNDWallet attaches the live LND client used for wallet management APIs.
func (s *Server) WithLNDWallet(client lndWalletClient) *Server {
	s.lndWallet = client
	return s
}

func (s *Server) handleLNDWalletStatus(w http.ResponseWriter, r *http.Request) {
	if s.lndWallet == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}
	status, err := buildLNDWalletStatus(r.Context(), s.lndWallet, s.repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLNDCreateAddress(w http.ResponseWriter, r *http.Request) {
	if s.lndWallet == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}

	var req createLNDAddressRequest
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
				return
			}
		}
	}

	address, err := s.lndWallet.NewAddress(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = fmt.Sprintf("Generated-%d", time.Now().Unix())
	}
	record := &db.WalletAddress{Label: label, Address: address}
	if err := s.repo.SaveWalletAddress(record); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, createLNDAddressResponse{
		Address:   address,
		Label:     label,
		Kind:      "onchain",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleLNDOpenChannel(w http.ResponseWriter, r *http.Request) {
	if s.lndWallet == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}

	var req openLNDChannelRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nodePubkey := strings.TrimSpace(req.NodePubkey)
	if nodePubkey == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("node_pubkey is required"))
		return
	}
	if req.LocalFundingAmountSat <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("local_funding_amount_sat must be positive"))
		return
	}
	if req.SatPerVbyte <= 0 {
		req.SatPerVbyte = 2
	}

	peerHost := strings.TrimSpace(req.PeerHost)
	if peerHost != "" {
		if err := s.lndWallet.ConnectPeer(r.Context(), nodePubkey, peerHost); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Errorf("connect peer: %w", err))
			return
		}
	}

	resp, err := s.lndWallet.OpenChannel(
		r.Context(),
		nodePubkey,
		req.LocalFundingAmountSat,
		req.PushSat,
		req.SatPerVbyte,
		req.Private,
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("open channel: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, openLNDChannelResponse{FundingTxid: resp.FundingTxid})
}

func (s *Server) handleLNDCloseChannel(w http.ResponseWriter, r *http.Request) {
	if s.lndWallet == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}

	var req closeLNDChannelRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	channelPoint := strings.TrimSpace(req.ChannelPoint)
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("channel_point is required"))
		return
	}
	if req.SatPerVbyte <= 0 {
		req.SatPerVbyte = 2
	}

	resp, err := s.lndWallet.CloseChannel(r.Context(), channelPoint, req.Force, req.SatPerVbyte)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("close channel: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, closeLNDChannelResponse{ClosingTxid: resp.ClosingTxid})
}

func decodeJSONBody(r *http.Request, out any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("request body is required")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	return nil
}

func buildLNDWalletStatus(
	ctx context.Context,
	client lndWalletClient,
	repo *db.TransactionRepository,
) (*lndWalletStatusResponse, error) {
	nodeInfo, err := client.NodeInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("lnd node info: %w", err)
	}

	channels, err := client.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("lnd list channels: %w", err)
	}
	pending, err := client.ListPendingChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("lnd pending channels: %w", err)
	}
	peers, err := client.ListPeers(ctx)
	if err != nil {
		return nil, fmt.Errorf("lnd list peers: %w", err)
	}
	onChain, err := client.ListWalletAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("lnd list addresses: %w", err)
	}

	peerByPubkey := make(map[string]lnd.Peer, len(peers))
	for _, peer := range peers {
		peerByPubkey[peer.PubKey] = peer
	}
	aliasCache := make(map[string]string)

	resp := &lndWalletStatusResponse{
		Node: lndNodeSummary{
			IdentityPubkey:    nodeInfo.IdentityPubkey,
			Alias:             strings.TrimSpace(nodeInfo.Alias),
			NumPeers:          nodeInfo.NumPeers,
			NumActiveChannels: nodeInfo.NumActiveChannels,
			SyncedToChain:     nodeInfo.SyncedToChain,
			SyncedToGraph:     nodeInfo.SyncedToGraph,
			BlockHeight:       nodeInfo.BlockHeight,
		},
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, peer := range peers {
		resp.Peers = append(resp.Peers, lndPeerSummary{
			PubKey:   peer.PubKey,
			Address:  peer.Address,
			Inbound:  peer.Inbound,
			SyncType: peer.SyncType,
		})
	}

	for _, ch := range channels {
		alias := lookupNodeAlias(ctx, client, aliasCache, ch.RemotePubkey)
		peerAddr := ""
		if peer, ok := peerByPubkey[ch.RemotePubkey]; ok {
			peerAddr = peer.Address
		}
		resp.Channels = append(resp.Channels, lndChannelSummary{
			RemotePubkey:     ch.RemotePubkey,
			RemoteAlias:      alias,
			PeerAddress:      peerAddr,
			CapacitySat:      parseSatString(ch.Capacity),
			LocalBalanceSat:  parseSatString(ch.LocalBalance),
			RemoteBalanceSat: parseSatString(ch.RemoteBalance),
			Active:           ch.Active,
			Private:          ch.Private,
			Health:           channelHealth(ch),
			ChannelPoint:     ch.ChannelPoint,
			TotalSentSat:     parseSatString(ch.TotalSatoshisSent),
			TotalReceivedSat: parseSatString(ch.TotalSatoshisReceived),
		})
	}

	if pending != nil {
		for _, item := range pending.PendingOpenChannels {
			ch := item.Channel
			resp.PendingChannels = append(resp.PendingChannels, lndPendingChannelSummary{
				RemotePubkey:  ch.RemoteNodePub,
				RemoteAlias:   lookupNodeAlias(ctx, client, aliasCache, ch.RemoteNodePub),
				CapacitySat:   parseSatString(ch.Capacity),
				LocalBalance:  parseSatString(ch.LocalBalance),
				RemoteBalance: parseSatString(ch.RemoteBalance),
				State:         "opening",
			})
		}
		for _, item := range pending.PendingClosingChannels {
			ch := item.Channel
			resp.PendingChannels = append(resp.PendingChannels, lndPendingChannelSummary{
				RemotePubkey:  ch.RemoteNodePub,
				RemoteAlias:   lookupNodeAlias(ctx, client, aliasCache, ch.RemoteNodePub),
				CapacitySat:   parseSatString(ch.Capacity),
				LocalBalance:  parseSatString(ch.LocalBalance),
				RemoteBalance: parseSatString(ch.RemoteBalance),
				State:         "closing",
			})
		}
		for _, item := range pending.PendingForceClosingChannels {
			ch := item.Channel
			resp.PendingChannels = append(resp.PendingChannels, lndPendingChannelSummary{
				RemotePubkey:  ch.RemoteNodePub,
				RemoteAlias:   lookupNodeAlias(ctx, client, aliasCache, ch.RemoteNodePub),
				CapacitySat:   parseSatString(ch.Capacity),
				LocalBalance:  parseSatString(ch.LocalBalance),
				RemoteBalance: parseSatString(ch.RemoteBalance),
				State:         "force_closing",
			})
		}
	}

	labelByAddress := map[string]string{}
	if repo != nil {
		stored, err := repo.ListWalletAddresses()
		if err != nil {
			return nil, err
		}
		for _, item := range stored {
			labelByAddress[item.Address] = item.Label
		}
	}

	resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
		Address: nodeInfo.IdentityPubkey,
		Kind:    "lightning",
		Label:   strings.TrimSpace(nodeInfo.Alias),
	})

	seenOnChain := make(map[string]struct{}, len(onChain))
	for _, addr := range onChain {
		seenOnChain[addr.Address] = struct{}{}
		resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
			Address:    addr.Address,
			Kind:       "onchain",
			Label:      labelByAddress[addr.Address],
			BalanceSat: parseSatString(addr.Balance),
			IsInternal: addr.IsInternal,
		})
	}
	for address, label := range labelByAddress {
		if _, ok := seenOnChain[address]; ok {
			continue
		}
		resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
			Address: address,
			Kind:    "onchain",
			Label:   label,
		})
	}

	return resp, nil
}

func lookupNodeAlias(
	ctx context.Context,
	client lndWalletClient,
	cache map[string]string,
	pubkey string,
) string {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return ""
	}
	if alias, ok := cache[pubkey]; ok {
		return alias
	}
	node, err := client.GraphNode(ctx, pubkey)
	if err != nil || node == nil {
		cache[pubkey] = ""
		return ""
	}
	alias := strings.TrimSpace(node.Node.Alias)
	cache[pubkey] = alias
	return alias
}

func channelHealth(ch lnd.Channel) string {
	if !ch.Active {
		return "inactive"
	}
	capacity := parseSatString(ch.Capacity)
	local := parseSatString(ch.LocalBalance)
	if capacity > 0 && local*100/capacity < 10 {
		return "low_outbound"
	}
	return "healthy"
}

func parseSatString(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
