package api

import (
	"context"
	"encoding/json"
	"errors"
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
		localFundingAmount, pushSat, satPerVbyte, targetConf int64,
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
	RemotePubkey     string `json:"remote_pubkey"`
	RemoteAlias      string `json:"remote_alias"`
	PeerAddress      string `json:"peer_address"`
	CapacitySat      int64  `json:"capacity_sat"`
	LocalBalanceSat  int64  `json:"local_balance_sat"`
	RemoteBalanceSat int64  `json:"remote_balance_sat"`
	Active           bool   `json:"active"`
	Private          bool   `json:"private"`
	Health           string `json:"health"`
	ChannelPoint     string `json:"channel_point"`
	TotalSentSat     int64  `json:"total_sent_sat"`
	TotalReceivedSat int64  `json:"total_received_sat"`
}

type lndPendingChannelSummary struct {
	RemotePubkey  string `json:"remote_pubkey"`
	RemoteAlias   string `json:"remote_alias"`
	CapacitySat   int64  `json:"capacity_sat"`
	LocalBalance  int64  `json:"local_balance_sat"`
	RemoteBalance int64  `json:"remote_balance_sat"`
	State         string `json:"state"`
}

type lndWalletAddressEntry struct {
	Address       string   `json:"address"`
	Kind          string   `json:"kind"`
	Label         string   `json:"label,omitempty"`
	BalanceSat    int64    `json:"balance_sat"`
	IsInternal    bool     `json:"is_internal,omitempty"`
	WhitelistedBy []string `json:"whitelisted_by,omitempty"`
}

type lndWalletStatusResponse struct {
	Node            lndNodeSummary             `json:"node"`
	Peers           []lndPeerSummary           `json:"peers"`
	Channels        []lndChannelSummary        `json:"channels"`
	PendingChannels []lndPendingChannelSummary `json:"pending_channels"`
	Addresses       []lndWalletAddressEntry    `json:"addresses"`
	CheckedAt       string                     `json:"checked_at"`
	Degraded        bool                       `json:"degraded"`
	Warnings        []string                   `json:"warnings"`
}

const lndWalletStatusTimeout = 7 * time.Second

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
	PresetID               string `json:"preset_id"`
	NodePubkey             string `json:"node_pubkey"`
	PeerHost               string `json:"peer_host"`
	LocalFundingAmountSat  int64  `json:"local_funding_amount_sat"`
	PushSat                int64  `json:"push_sat"`
	SatPerVbyte            int64  `json:"sat_per_vbyte"`
	TargetConf             int64  `json:"target_conf"`
	Private                bool   `json:"private"`
	AllowAdditionalChannel bool   `json:"allow_additional_channel"`
}

type openLNDChannelResponse struct {
	FundingTxid  string `json:"funding_txid"`
	ChannelPoint string `json:"channel_point"`
	Status       string `json:"status"`
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
	s.setLNDWallet(client)
	return s
}

func (s *Server) setLNDWallet(client lndWalletClient) {
	s.lndWalletMu.Lock()
	s.lndWallet = client
	s.lndWalletMu.Unlock()
}

func (s *Server) currentLNDWallet() lndWalletClient {
	s.lndWalletMu.RLock()
	defer s.lndWalletMu.RUnlock()
	return s.lndWallet
}

func (s *Server) handleLNDWalletStatus(w http.ResponseWriter, r *http.Request) {
	client := s.currentLNDWallet()
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lndWalletStatusTimeout)
	defer cancel()
	status, err := buildLNDWalletStatus(ctx, client, s.currentRepo())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLNDCreateAddress(w http.ResponseWriter, r *http.Request) {
	client := s.currentLNDWallet()
	if client == nil {
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

	address, err := client.NewAddress(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = fmt.Sprintf("Generated-%d", time.Now().Unix())
	}
	record := &db.WalletAddress{Label: label, Address: address}
	repo := s.currentRepo()
	if repo == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database is not ready"))
		return
	}
	if err := repo.SaveWalletAddress(record); err != nil {
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
	client := s.currentLNDWallet()
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}

	var req openLNDChannelRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	presetID := strings.TrimSpace(req.PresetID)
	nodePubkey := strings.TrimSpace(req.NodePubkey)
	peerHost := strings.TrimSpace(req.PeerHost)
	if presetID != "" {
		preset, ok := enabledChannelPreset(presetID)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown or disabled channel preset %q", presetID))
			return
		}
		if nodePubkey != "" || peerHost != "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("preset_id cannot be combined with manual node details"))
			return
		}
		nodePubkey = preset.NodePubkey
		peerHost = preset.PeerHost
		if req.LocalFundingAmountSat < preset.MinFundingAmountSat {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s requires at least %d sats", preset.Name, preset.MinFundingAmountSat))
			return
		}
		if preset.MaxFundingAmountSat > 0 && req.LocalFundingAmountSat > preset.MaxFundingAmountSat {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s accepts at most %d sats", preset.Name, preset.MaxFundingAmountSat))
			return
		}
	}
	if nodePubkey == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("node_pubkey is required"))
		return
	}
	if req.LocalFundingAmountSat <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("local_funding_amount_sat must be positive"))
		return
	}
	if req.PushSat < 0 || req.PushSat >= req.LocalFundingAmountSat {
		writeError(w, http.StatusBadRequest, fmt.Errorf("push_sat must be non-negative and smaller than local_funding_amount_sat"))
		return
	}
	if req.SatPerVbyte < 0 || req.TargetConf < 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("fee settings must be positive"))
		return
	}
	if req.SatPerVbyte == 0 && req.TargetConf == 0 {
		req.TargetConf = 3
	}

	channels, err := client.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("list channels: %w", err))
		return
	}
	pending, err := client.ListPendingChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("list pending channels: %w", err))
		return
	}
	if hasChannelWithPeer(channels, pending, nodePubkey) && !req.AllowAdditionalChannel {
		writeError(w, http.StatusConflict, fmt.Errorf("an open or pending channel already exists for this node; confirm opening an additional channel"))
		return
	}

	peerRetained := false
	if peerHost != "" && !isConnectedPeer(r.Context(), client, nodePubkey) {
		if err := client.ConnectPeer(r.Context(), nodePubkey, peerHost); err != nil {
			if !isConnectedPeer(r.Context(), client, nodePubkey) {
				writeError(w, http.StatusBadGateway, fmt.Errorf("connect peer: %w", err))
				return
			}
		}
		peerRetained = true
	}

	resp, err := client.OpenChannel(
		r.Context(),
		nodePubkey,
		req.LocalFundingAmountSat,
		req.PushSat,
		req.SatPerVbyte,
		req.TargetConf,
		req.Private,
	)
	if err != nil {
		if peerRetained {
			writeError(w, http.StatusBadGateway, fmt.Errorf("open channel: %w (peer connection was kept for a retry)", err))
		} else {
			writeError(w, http.StatusBadGateway, fmt.Errorf("open channel: %w", err))
		}
		return
	}

	channelPoint := ""
	if resp.FundingTxid != "" {
		channelPoint = fmt.Sprintf("%s:%d", resp.FundingTxid, resp.OutputIndex)
	}
	writeJSON(w, http.StatusOK, openLNDChannelResponse{
		FundingTxid:  resp.FundingTxid,
		ChannelPoint: channelPoint,
		Status:       "pending",
	})
}

func isConnectedPeer(ctx context.Context, client lndWalletClient, pubkey string) bool {
	peers, err := client.ListPeers(ctx)
	if err != nil {
		return false
	}
	for _, peer := range peers {
		if strings.EqualFold(strings.TrimSpace(peer.PubKey), pubkey) {
			return true
		}
	}
	return false
}

func hasChannelWithPeer(channels []lnd.Channel, pending *lnd.PendingChannelsResponse, pubkey string) bool {
	for _, channel := range channels {
		if strings.EqualFold(strings.TrimSpace(channel.RemotePubkey), pubkey) {
			return true
		}
	}
	if pending == nil {
		return false
	}
	for _, entry := range pending.PendingOpenChannels {
		if strings.EqualFold(strings.TrimSpace(entry.Channel.RemoteNodePub), pubkey) {
			return true
		}
	}
	for _, entry := range pending.PendingClosingChannels {
		if strings.EqualFold(strings.TrimSpace(entry.Channel.RemoteNodePub), pubkey) {
			return true
		}
	}
	for _, entry := range pending.PendingForceClosingChannels {
		if strings.EqualFold(strings.TrimSpace(entry.Channel.RemoteNodePub), pubkey) {
			return true
		}
	}
	return false
}

func (s *Server) handleLNDCloseChannel(w http.ResponseWriter, r *http.Request) {
	client := s.currentLNDWallet()
	if client == nil {
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

	resp, err := client.CloseChannel(r.Context(), channelPoint, req.Force, req.SatPerVbyte)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("close channel: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, closeLNDChannelResponse{ClosingTxid: resp.ClosingTxid})
}

type lndWalletStateResponse struct {
	LNDReachable  bool   `json:"lnd_reachable"`
	LNDState      string `json:"lnd_state"`
	WalletExists  bool   `json:"wallet_exists"`
	PasswordSet   bool   `json:"password_set"`
	ClientReady   bool   `json:"client_ready"`
	CanCreate     bool   `json:"can_create"`
	LastInitError string `json:"last_init_error,omitempty"`
}

func (s *Server) handleServiceVersions(w http.ResponseWriter, r *http.Request) {
	versions := ServiceVersions{}
	if s.serviceVersions != nil {
		versions = s.serviceVersions(r.Context())
	}
	if client := s.currentLNDWallet(); client != nil {
		// Version discovery is informational and must not make this endpoint
		// exceed the dexMonitor proxy timeout when LND is degraded.
		probeCtx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if info, err := client.NodeInfo(probeCtx); err == nil {
			versions.LND = info.Version
		}
	}
	writeJSON(w, http.StatusOK, versions)
}

type lndWalletInitResponse struct {
	PrimaryAddress     string `json:"primary_address"`
	AlreadyInitialized bool   `json:"already_initialized,omitempty"`
}

// handleLNDWalletState reports whether a wallet exists and can be created. It drives the
// frontend init wizard and works before any wallet exists (no live client required).
func (s *Server) handleLNDWalletState(w http.ResponseWriter, r *http.Request) {
	resp := lndWalletStateResponse{
		ClientReady: s.currentLNDWallet() != nil,
	}

	if repo := s.currentRepo(); repo != nil {
		if password, _, err := repo.LNDWalletCredentials(r.Context()); err == nil {
			resp.PasswordSet = strings.TrimSpace(password) != ""
		}
	}

	if s.lndStateProbe != nil {
		if state, err := s.lndStateProbe(r.Context()); err == nil {
			resp.LNDReachable = true
			resp.LNDState = state
			resp.WalletExists = state != "NON_EXISTING"
			resp.CanCreate = state == "NON_EXISTING"
		}
	}
	if !resp.ClientReady && s.lndLastInitError != nil {
		resp.LastInitError = strings.TrimSpace(s.lndLastInitError())
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleLNDWalletInit creates the wallet on demand using the password already stored in the
// database, then injects the live client into the running server and health checker.
func (s *Server) handleLNDWalletInit(w http.ResponseWriter, r *http.Request) {
	if s.lndInit == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet init is not configured"))
		return
	}

	s.lndInitMu.Lock()
	defer s.lndInitMu.Unlock()

	if s.currentLNDWallet() != nil {
		address := ""
		if repo := s.currentRepo(); repo != nil {
			if wallet, err := repo.GetPrimaryWalletAddress(); err == nil && wallet != nil {
				address = wallet.Address
			}
		}
		writeJSON(w, http.StatusOK, lndWalletInitResponse{PrimaryAddress: address, AlreadyInitialized: true})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, err := s.lndInit(ctx)
	if err != nil {
		writeError(w, lndInitErrorStatus(err), err)
		return
	}

	writeJSON(w, http.StatusCreated, lndWalletInitResponse{PrimaryAddress: result.PrimaryAddress})
}

// InitializeLND executes the same serialized path used by the UI endpoint.
// Startup callers use it so an automatic retry can never race an operator's
// explicit unlock/init request.
func (s *Server) InitializeLND(ctx context.Context) (LNDInitResult, error) {
	if s.lndInit == nil {
		return LNDInitResult{}, fmt.Errorf("LND wallet init is not configured")
	}
	s.lndInitMu.Lock()
	defer s.lndInitMu.Unlock()
	if s.currentLNDWallet() != nil {
		return LNDInitResult{}, nil
	}
	return s.lndInit(ctx)
}

// lndInitErrorStatus maps wallet-init failures to HTTP status codes.
func lndInitErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrLNDWalletPasswordMissing),
		errors.Is(err, ErrLNDRestoreSecretInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrLNDConfigIncomplete):
		return http.StatusServiceUnavailable
	case strings.Contains(strings.ToLower(err.Error()), "invalid passphrase"):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
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

func decodeOptionalJSONBody(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
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
	type rpcResult struct {
		kind  string
		value any
		err   error
	}
	results := make(chan rpcResult, 5)
	go func() { value, err := client.NodeInfo(ctx); results <- rpcResult{"node info", value, err} }()
	go func() { value, err := client.ListChannels(ctx); results <- rpcResult{"channels", value, err} }()
	go func() {
		value, err := client.ListPendingChannels(ctx)
		results <- rpcResult{"pending channels", value, err}
	}()
	go func() { value, err := client.ListPeers(ctx); results <- rpcResult{"peers", value, err} }()
	go func() { value, err := client.ListWalletAddresses(ctx); results <- rpcResult{"addresses", value, err} }()

	nodeInfo := &lnd.NodeInfoResponse{}
	var channels []lnd.Channel
	var pending *lnd.PendingChannelsResponse
	var peers []lnd.Peer
	var onChain []lnd.WalletAddressProperty
	warnings := make([]string, 0)
	for received := 0; received < 5; received++ {
		select {
		case result := <-results:
			if result.err != nil {
				warnings = append(warnings, fmt.Sprintf("lnd %s unavailable: %v", result.kind, result.err))
				continue
			}
			switch result.kind {
			case "node info":
				if value := result.value.(*lnd.NodeInfoResponse); value != nil {
					nodeInfo = value
				}
			case "channels":
				channels = result.value.([]lnd.Channel)
			case "pending channels":
				pending = result.value.(*lnd.PendingChannelsResponse)
			case "peers":
				peers = result.value.([]lnd.Peer)
			case "addresses":
				onChain = result.value.([]lnd.WalletAddressProperty)
			}
		case <-ctx.Done():
			warnings = append(warnings, "LND wallet data is still loading; some fields may be temporarily unavailable")
			received = 5
		}
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
		// Initialize as empty (not nil) so JSON encodes [] rather than null for a
		// wallet with no peers/channels/addresses; the frontend types assume arrays.
		Peers:           []lndPeerSummary{},
		Channels:        []lndChannelSummary{},
		PendingChannels: []lndPendingChannelSummary{},
		Addresses:       []lndWalletAddressEntry{},
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Degraded:        len(warnings) > 0,
		Warnings:        warnings,
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
	whitelistedByAddress := map[string][]string{}
	primaryAddress := ""
	if repo != nil {
		stored, err := repo.ListWalletAddresses()
		if err != nil {
			return nil, err
		}
		for _, item := range stored {
			labelByAddress[item.Address] = item.Label
			if item.Label == "Primary" {
				primaryAddress = item.Address
			}
			if provider := strings.TrimSpace(item.Provider); provider != "" {
				whitelistedByAddress[item.Address] = append(whitelistedByAddress[item.Address], provider)
			}
		}
	}

	if identityPubkey := strings.TrimSpace(nodeInfo.IdentityPubkey); identityPubkey != "" {
		resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
			Address: identityPubkey,
			Kind:    "lightning",
			Label:   strings.TrimSpace(nodeInfo.Alias),
		})
	}

	seenOnChain := make(map[string]struct{}, len(onChain))
	for _, addr := range onChain {
		seenOnChain[addr.Address] = struct{}{}
		balance := parseSatString(addr.Balance)
		if !shouldDisplayWalletAddress(addr.Address, primaryAddress, balance) {
			continue
		}
		resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
			Address:       addr.Address,
			Kind:          "onchain",
			Label:         labelByAddress[addr.Address],
			BalanceSat:    balance,
			IsInternal:    addr.IsInternal,
			WhitelistedBy: whitelistedByAddress[addr.Address],
		})
	}
	if primaryAddress != "" {
		if _, ok := seenOnChain[primaryAddress]; !ok {
		resp.Addresses = append(resp.Addresses, lndWalletAddressEntry{
			Address:       primaryAddress,
			Kind:          "onchain",
			Label:         labelByAddress[primaryAddress],
			WhitelistedBy: whitelistedByAddress[primaryAddress],
		})
		}
	}

	return resp, nil
}

// shouldDisplayWalletAddress keeps the wallet panel focused on the configured
// deposit address and actual funds. LND retains derived look-ahead addresses
// for recovery and scanning, which are operational metadata rather than a
// useful inventory list for operators.
func shouldDisplayWalletAddress(address, primaryAddress string, balance int64) bool {
	return address == primaryAddress || balance > 0
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
