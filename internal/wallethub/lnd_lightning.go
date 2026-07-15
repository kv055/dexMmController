package lnd

import (
	"context"
	"fmt"
)

// ── types ─────────────────────────────────────────────────────────────────────

// OpenChannelRequest contains the LND fields for opening a Lightning channel.
type OpenChannelRequest struct {
	// NodePubkey is the node pubkey value for the open channel request.
	NodePubkey string `json:"node_pubkey"`
	// LocalFundingAmount is the local funding amount value for the open channel request.
	LocalFundingAmount int64 `json:"local_funding_amount"`
	// PushSat records the relevant time for the open channel request.
	PushSat int64 `json:"push_sat"`
	// SatPerVbyte is the sat per vbyte value for the open channel request.
	SatPerVbyte int64 `json:"sat_per_vbyte"`
	// Private reports whether the open channel request condition is true.
	Private bool `json:"private"`
}

// OpenChannelResponse contains the funding transaction returned by LND.
type OpenChannelResponse struct {
	// FundingTxid is the funding txid value for the open channel response.
	FundingTxid string `json:"funding_txid_str"`
}

// CloseChannelResponse represents a close channel response.
type CloseChannelResponse struct {
	// ClosingTxid is the closing txid value for the close channel response.
	ClosingTxid string `json:"closing_txid"`
}

// Channel represents a channel.
type Channel struct {
	// Active reports whether the channel condition is true.
	Active bool `json:"active"`
	// RemotePubkey is the remote pubkey value for the channel.
	RemotePubkey string `json:"remote_pubkey"`
	// ChannelPoint is the channel point value for the channel.
	ChannelPoint string `json:"channel_point"`
	// Capacity is the capacity value for the channel.
	Capacity string `json:"capacity"`
	// LocalBalance is the local balance value for the channel.
	LocalBalance string `json:"local_balance"`
	// RemoteBalance is the remote balance value for the channel.
	RemoteBalance string `json:"remote_balance"`
	// CommitFee is the fee value reported for the channel.
	CommitFee string `json:"commit_fee"`
	// Private reports whether the channel condition is true.
	Private bool `json:"private"`
	// TotalSatoshisSent is the total satoshis sent value for the channel.
	TotalSatoshisSent string `json:"total_satoshis_sent"`
	// TotalSatoshisReceived is the total satoshis received value for the channel.
	TotalSatoshisReceived string `json:"total_satoshis_received"`
}

// ListChannelsResponse represents a list channels response.
type ListChannelsResponse struct {
	// Channels is the channels value for the list channels response.
	Channels []Channel `json:"channels"`
}

// PendingChannel represents a pending channel.
type PendingChannel struct {
	// RemoteNodePub is the remote node pub value for the pending channel.
	RemoteNodePub string `json:"remote_node_pub"`
	// ChannelPoint is the channel point value for the pending channel.
	ChannelPoint string `json:"channel_point"`
	// Capacity is the capacity value for the pending channel.
	Capacity string `json:"capacity"`
	// LocalBalance is the local balance value for the pending channel.
	LocalBalance string `json:"local_balance"`
	// RemoteBalance is the remote balance value for the pending channel.
	RemoteBalance string `json:"remote_balance"`
}

// PendingChannelsResponse represents a pending channels response.
type PendingChannelsResponse struct {
	// PendingOpenChannels is the pending open channels value for the pending channels response.
	PendingOpenChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_open_channels"`
	// PendingClosingChannels is the pending closing channels value for the pending channels response.
	PendingClosingChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_closing_channels"`
	// PendingForceClosingChannels is the pending force closing channels value for the pending channels response.
	PendingForceClosingChannels []struct {
		Channel PendingChannel `json:"channel"`
	} `json:"pending_force_closing_channels"`
}

// Invoice is the LND representation of a Lightning invoice.
type Invoice struct {
	// Memo carries a human-readable message for the invoice.
	Memo string `json:"memo"`
	// RHash is the rhash value for the invoice.
	RHash string `json:"r_hash"`
	// Value is the value value for the invoice.
	Value string `json:"value"`
	// ValueMsat is the amount in millisatoshis.
	ValueMsat string `json:"value_msat"`
	// Settled is the settled value for the invoice.
	Settled bool `json:"settled"`
	// CreationDate records the relevant time for the invoice.
	CreationDate string `json:"creation_date"`
	// SettleDate records the relevant time for the invoice.
	SettleDate string `json:"settle_date"`
	// PaymentRequest is the payment request value for the invoice.
	PaymentRequest string `json:"payment_request"`
	// Expiry is the expiry value for the invoice.
	Expiry string `json:"expiry"`
	// AmtPaidSat records the relevant time for the invoice.
	AmtPaidSat string `json:"amt_paid_sat"`
	// AmtPaidMsat is the amount in millisatoshis.
	AmtPaidMsat string `json:"amt_paid_msat"`
	// State is the state value for the invoice.
	State string `json:"state"` // OPEN, SETTLED, CANCELED, ACCEPTED
}

// ListInvoicesResponse represents a list invoices response.
type ListInvoicesResponse struct {
	// Invoices is the invoices value for the list invoices response.
	Invoices []Invoice `json:"invoices"`
}

// Payment represents a payment.
type Payment struct {
	// PaymentHash is the payment hash value for the payment.
	PaymentHash string `json:"payment_hash"`
	// ValueSat records the relevant time for the payment.
	ValueSat string `json:"value_sat"`
	// ValueMsat is the amount in millisatoshis.
	ValueMsat string `json:"value_msat"`
	// PaymentPreimage is the payment preimage value for the payment.
	PaymentPreimage string `json:"payment_preimage"`
	// Status is the current status of the payment.
	Status string `json:"status"` // UNKNOWN, IN_FLIGHT, SUCCEEDED, FAILED
	// FeeSat records the relevant time for the payment.
	FeeSat string `json:"fee_sat"`
	// CreationDate records the relevant time for the payment.
	CreationDate string `json:"creation_date"`
	// PaymentRequest is the payment request value for the payment.
	PaymentRequest string `json:"payment_request"`
}

// ListPaymentsResponse represents a list payments response.
type ListPaymentsResponse struct {
	// Payments is the payments value for the list payments response.
	Payments []Payment `json:"payments"`
}

// NodeInfoResponse represents a node info response.
type NodeInfoResponse struct {
	// Version is the running LND release.
	Version string `json:"version"`
	// IdentityPubkey is the identity pubkey value for the node info response.
	IdentityPubkey string `json:"identity_pubkey"`
	// Alias is the alias value for the node info response.
	Alias string `json:"alias"`
	// NumPeers is the count associated with the node info response.
	NumPeers uint32 `json:"num_peers"`
	// NumActiveChannels is the count associated with the node info response.
	NumActiveChannels uint32 `json:"num_active_channels"`
	// NumInactiveChannels is the count associated with the node info response.
	NumInactiveChannels uint32 `json:"num_inactive_channels"`
	// NumPendingChannels is the count associated with the node info response.
	NumPendingChannels uint32 `json:"num_pending_channels"`
	// SyncedToChain reports whether the node info response condition is true.
	SyncedToChain bool `json:"synced_to_chain"`
	// SyncedToGraph reports whether the node info response condition is true.
	SyncedToGraph bool `json:"synced_to_graph"`
	// BlockHeight is the block height value for the node info response.
	BlockHeight uint32 `json:"block_height"`
}

// ConnectPeerRequest represents a connect peer request.
type ConnectPeerRequest struct {
	// Addr is the wallet or peer address used by the connect peer request.
	Addr struct {
		Pubkey string `json:"pubkey"`
		Host   string `json:"host"`
	} `json:"addr"`
	// Perm is the perm value for the connect peer request.
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
