package lnd

import (
	"context"
	"fmt"
)

// ── types ─────────────────────────────────────────────────────────────────────

type OpenChannelRequest struct {
	NodePubkey         string `json:"node_pubkey"`
	LocalFundingAmount int64  `json:"local_funding_amount"`
	PushSat            int64  `json:"push_sat"`
	SatPerVbyte        int64  `json:"sat_per_vbyte"`
	Private            bool   `json:"private"`
}

type OpenChannelResponse struct {
	FundingTxid string `json:"funding_txid_str"`
}

type CloseChannelResponse struct {
	ClosingTxid string `json:"closing_txid"`
}

type Channel struct {
	Active                bool   `json:"active"`
	RemotePubkey          string `json:"remote_pubkey"`
	ChannelPoint          string `json:"channel_point"`
	Capacity              string `json:"capacity"`
	LocalBalance          string `json:"local_balance"`
	RemoteBalance         string `json:"remote_balance"`
	CommitFee             string `json:"commit_fee"`
	Private               bool   `json:"private"`
	TotalSatoshisSent     string `json:"total_satoshis_sent"`
	TotalSatoshisReceived string `json:"total_satoshis_received"`
}

type ListChannelsResponse struct {
	Channels []Channel `json:"channels"`
}

type PendingChannel struct {
	RemoteNodePub string `json:"remote_node_pub"`
	ChannelPoint  string `json:"channel_point"`
	Capacity      string `json:"capacity"`
	LocalBalance  string `json:"local_balance"`
	RemoteBalance string `json:"remote_balance"`
}

type PendingChannelsResponse struct {
	PendingOpenChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_open_channels"`
	PendingClosingChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_closing_channels"`
	PendingForceClosingChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_force_closing_channels"`
}

type Invoice struct {
	Memo           string `json:"memo"`
	RHash          string `json:"r_hash"`
	Value          string `json:"value"`
	ValueMsat      string `json:"value_msat"`
	Settled        bool   `json:"settled"`
	CreationDate   string `json:"creation_date"`
	SettleDate     string `json:"settle_date"`
	PaymentRequest string `json:"payment_request"`
	Expiry         string `json:"expiry"`
	AmtPaidSat     string `json:"amt_paid_sat"`
	AmtPaidMsat    string `json:"amt_paid_msat"`
	State          string `json:"state"` // OPEN, SETTLED, CANCELED, ACCEPTED
}

type ListInvoicesResponse struct {
	Invoices []Invoice `json:"invoices"`
}

type Payment struct {
	PaymentHash     string `json:"payment_hash"`
	ValueSat        string `json:"value_sat"`
	ValueMsat       string `json:"value_msat"`
	PaymentPreimage string `json:"payment_preimage"`
	Status          string `json:"status"` // UNKNOWN, IN_FLIGHT, SUCCEEDED, FAILED
	FeeSat          string `json:"fee_sat"`
	CreationDate    string `json:"creation_date"`
	PaymentRequest  string `json:"payment_request"`
}

type ListPaymentsResponse struct {
	Payments []Payment `json:"payments"`
}

type NodeInfoResponse struct {
	IdentityPubkey      string `json:"identity_pubkey"`
	Alias               string `json:"alias"`
	NumPeers            uint32 `json:"num_peers"`
	NumActiveChannels   uint32 `json:"num_active_channels"`
	NumInactiveChannels uint32 `json:"num_inactive_channels"`
	NumPendingChannels  uint32 `json:"num_pending_channels"`
	SyncedToChain       bool   `json:"synced_to_chain"`
	SyncedToGraph       bool   `json:"synced_to_graph"`
	BlockHeight         uint32 `json:"block_height"`
}

type ConnectPeerRequest struct {
	Addr struct {
		Pubkey string `json:"pubkey"`
		Host   string `json:"host"`
	} `json:"addr"`
	Perm bool `json:"perm"`
}

// ── node ──────────────────────────────────────────────────────────────────────

// NodeInfo returns info about your LND node.
func (c *Client) NodeInfo(ctx context.Context) (*NodeInfoResponse, error) {
	var resp NodeInfoResponse
	if err := c.get(ctx, "/v1/getinfo", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ConnectPeer connects to a Lightning peer.
// Required before opening a channel with them.
func (c *Client) ConnectPeer(ctx context.Context, pubkey, host string) error {
	req := ConnectPeerRequest{Perm: true}
	req.Addr.Pubkey = pubkey
	req.Addr.Host = host
	return c.post(ctx, "/v1/peers", req, nil)
}

// ── channels ──────────────────────────────────────────────────────────────────

// OpenChannel opens a channel with a peer.
// You must be connected to the peer first via ConnectPeer().
func (c *Client) OpenChannel(
	ctx context.Context,
	nodePubkey string,
	localFundingAmount, pushSat, satPerVbyte int64,
	private bool,
) (*OpenChannelResponse, error) {
	req := OpenChannelRequest{
		NodePubkey:         nodePubkey,
		LocalFundingAmount: localFundingAmount,
		PushSat:            pushSat,
		SatPerVbyte:        satPerVbyte,
		Private:            private,
	}
	var resp OpenChannelResponse
	if err := c.post(ctx, "/v1/channels", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CloseChannel closes an existing channel.
// channelPoint is in the format "txid:output_index" from ListChannels().
func (c *Client) CloseChannel(
	ctx context.Context,
	channelPoint string,
	force bool,
	satPerVbyte int64,
) (*CloseChannelResponse, error) {
	txid, outputIndex, err := parseChannelPoint(channelPoint)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf(
		"/v1/channels/%s/%d?force=%t&sat_per_vbyte=%d",
		txid, outputIndex, force, satPerVbyte,
	)

	var resp CloseChannelResponse
	if err := c.delete(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListChannels returns all open channels.
func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	var resp ListChannelsResponse
	if err := c.get(ctx, "/v1/channels", &resp); err != nil {
		return nil, err
	}
	return resp.Channels, nil
}

// ListPendingChannels returns channels that are pending open or close.
func (c *Client) ListPendingChannels(ctx context.Context) (*PendingChannelsResponse, error) {
	var resp PendingChannelsResponse
	if err := c.get(ctx, "/v1/channels/pending", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ── invoices ──────────────────────────────────────────────────────────────────

// GetInvoice returns a single invoice by its r_hash.
func (c *Client) GetInvoice(ctx context.Context, rHash string) (*Invoice, error) {
	var resp Invoice
	if err := c.get(ctx, "/v1/invoice/"+rHash, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListInvoices returns all invoices.
// Set pendingOnly=true to only return unpaid invoices.
func (c *Client) ListInvoices(
	ctx context.Context,
	pendingOnly bool,
) ([]Invoice, error) {
	path := fmt.Sprintf("/v1/invoices?pending_only=%t", pendingOnly)
	var resp ListInvoicesResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Invoices, nil
}

// ── payments ──────────────────────────────────────────────────────────────────

// ListPayments returns all outgoing Lightning payments.
// Set includeIncomplete=true to also return in-flight or failed payments.
func (c *Client) ListPayments(
	ctx context.Context,
	includeIncomplete bool,
) ([]Payment, error) {
	path := fmt.Sprintf("/v1/payments?include_incomplete=%t", includeIncomplete)
	var resp ListPaymentsResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Payments, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseChannelPoint(channelPoint string) (string, int, error) {
	var txid string
	var outputIndex int
	_, err := fmt.Sscanf(channelPoint, "%s:%d", &txid, &outputIndex)
	if err != nil {
		return "", 0, fmt.Errorf(
			"invalid channel point %q, expected txid:index",
			channelPoint,
		)
	}
	return txid, outputIndex, nil
}
