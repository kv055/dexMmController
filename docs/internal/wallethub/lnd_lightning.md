# `internal/wallethub/lnd_lightning.go`

Source package: `lnd`  
Source lines: `331`

## Package Purpose

This file contributes to the `lnd` package.

## File Role

This file defines 13 type(s), implements 10 function or method entry point(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `OpenChannelRequest`

OpenChannelRequest contains the LND fields for opening a Lightning channel.

Fields:

- `NodePubkey` `string`: NodePubkey is the node pubkey value for the open channel request.
- `LocalFundingAmount` `int64`: LocalFundingAmount is the local funding amount value for the open channel request.
- `PushSat` `int64`: PushSat records the relevant time for the open channel request.
- `SatPerVbyte` `int64`: SatPerVbyte is the sat per vbyte value for the open channel request.
- `Private` `bool`: Private reports whether the open channel request condition is true.

### `OpenChannelResponse`

OpenChannelResponse contains the funding transaction returned by LND.

Fields:

- `FundingTxid` `string`: FundingTxid is the funding txid value for the open channel response.

### `CloseChannelResponse`

CloseChannelResponse represents a close channel response.

Fields:

- `ClosingTxid` `string`: ClosingTxid is the closing txid value for the close channel response.

### `Channel`

Channel represents a channel.

Fields:

- `Active` `bool`: Active reports whether the channel condition is true.
- `RemotePubkey` `string`: RemotePubkey is the remote pubkey value for the channel.
- `ChannelPoint` `string`: ChannelPoint is the channel point value for the channel.
- `Capacity` `string`: Capacity is the capacity value for the channel.
- `LocalBalance` `string`: LocalBalance is the local balance value for the channel.
- `RemoteBalance` `string`: RemoteBalance is the remote balance value for the channel.
- `CommitFee` `string`: CommitFee is the fee value reported for the channel.
- `Private` `bool`: Private reports whether the channel condition is true.
- `TotalSatoshisSent` `string`: TotalSatoshisSent is the total satoshis sent value for the channel.
- `TotalSatoshisReceived` `string`: TotalSatoshisReceived is the total satoshis received value for the channel.

### `ListChannelsResponse`

ListChannelsResponse represents a list channels response.

Fields:

- `Channels` `[]Channel`: Channels is the channels value for the list channels response.

### `PendingChannel`

PendingChannel represents a pending channel.

Fields:

- `RemoteNodePub` `string`: RemoteNodePub is the remote node pub value for the pending channel.
- `ChannelPoint` `string`: ChannelPoint is the channel point value for the pending channel.
- `Capacity` `string`: Capacity is the capacity value for the pending channel.
- `LocalBalance` `string`: LocalBalance is the local balance value for the pending channel.
- `RemoteBalance` `string`: RemoteBalance is the remote balance value for the pending channel.

### `PendingChannelsResponse`

PendingChannelsResponse represents a pending channels response.

Fields:

- `PendingOpenChannels` `[]struct { Channel PendingChannel `json:"channel"` }`: PendingOpenChannels is the pending open channels value for the pending channels response.
- `PendingClosingChannels` `[]struct { Channel PendingChannel `json:"channel"` }`: PendingClosingChannels is the pending closing channels value for the pending channels response.
- `PendingForceClosingChannels` `[]struct { Channel PendingChannel `json:"channel"` }`: PendingForceClosingChannels is the pending force closing channels value for the pending channels response.

### `Invoice`

Invoice is the LND representation of a Lightning invoice.

Fields:

- `Memo` `string`: Memo carries a human-readable message for the invoice.
- `RHash` `string`: RHash is the rhash value for the invoice.
- `Value` `string`: Value is the value value for the invoice.
- `ValueMsat` `string`: ValueMsat is the amount in millisatoshis.
- `Settled` `bool`: Settled is the settled value for the invoice.
- `CreationDate` `string`: CreationDate records the relevant time for the invoice.
- `SettleDate` `string`: SettleDate records the relevant time for the invoice.
- `PaymentRequest` `string`: PaymentRequest is the payment request value for the invoice.
- `Expiry` `string`: Expiry is the expiry value for the invoice.
- `AmtPaidSat` `string`: AmtPaidSat records the relevant time for the invoice.
- `AmtPaidMsat` `string`: AmtPaidMsat is the amount in millisatoshis.
- `State` `string`: State is the state value for the invoice.

### `ListInvoicesResponse`

ListInvoicesResponse represents a list invoices response.

Fields:

- `Invoices` `[]Invoice`: Invoices is the invoices value for the list invoices response.

### `Payment`

Payment represents a payment.

Fields:

- `PaymentHash` `string`: PaymentHash is the payment hash value for the payment.
- `ValueSat` `string`: ValueSat records the relevant time for the payment.
- `ValueMsat` `string`: ValueMsat is the amount in millisatoshis.
- `PaymentPreimage` `string`: PaymentPreimage is the payment preimage value for the payment.
- `Status` `string`: Status is the current status of the payment.
- `FeeSat` `string`: FeeSat records the relevant time for the payment.
- `CreationDate` `string`: CreationDate records the relevant time for the payment.
- `PaymentRequest` `string`: PaymentRequest is the payment request value for the payment.

### `ListPaymentsResponse`

ListPaymentsResponse represents a list payments response.

Fields:

- `Payments` `[]Payment`: Payments is the payments value for the list payments response.

### `NodeInfoResponse`

NodeInfoResponse represents a node info response.

Fields:

- `IdentityPubkey` `string`: IdentityPubkey is the identity pubkey value for the node info response.
- `Alias` `string`: Alias is the alias value for the node info response.
- `NumPeers` `uint32`: NumPeers is the count associated with the node info response.
- `NumActiveChannels` `uint32`: NumActiveChannels is the count associated with the node info response.
- `NumInactiveChannels` `uint32`: NumInactiveChannels is the count associated with the node info response.
- `NumPendingChannels` `uint32`: NumPendingChannels is the count associated with the node info response.
- `SyncedToChain` `bool`: SyncedToChain reports whether the node info response condition is true.
- `SyncedToGraph` `bool`: SyncedToGraph reports whether the node info response condition is true.
- `BlockHeight` `uint32`: BlockHeight is the block height value for the node info response.

### `ConnectPeerRequest`

ConnectPeerRequest represents a connect peer request.

Fields:

- `Addr` `struct { Pubkey string `json:"pubkey"` Host string `json:"host"` }`: Addr is the wallet or peer address used by the connect peer request.
- `Perm` `bool`: Perm is the perm value for the connect peer request.

## Functions and Methods

### `(*Client).NodeInfo`

NodeInfo returns info about your LND node.

Signature: `func (c *Client) NodeInfo(ctx context.Context) (*NodeInfoResponse, error)`

### `(*Client).ConnectPeer`

ConnectPeer connects to a Lightning peer.
Required before opening a channel with them.

Signature: `func (c *Client) ConnectPeer(ctx context.Context, pubkey, host string) error`

### `(*Client).OpenChannel`

OpenChannel opens a channel with a peer.
You must be connected to the peer first via ConnectPeer().

Signature: `func (c *Client) OpenChannel( ctx context.Context, nodePubkey string, localFundingAmount, pushSat, satPerVbyte int64, private bool, ) (*OpenChannelResponse, error)`

### `(*Client).CloseChannel`

CloseChannel closes an existing channel.
channelPoint is in the format "txid:output_index" from ListChannels().

Signature: `func (c *Client) CloseChannel( ctx context.Context, channelPoint string, force bool, satPerVbyte int64, ) (*CloseChannelResponse, error)`

### `(*Client).ListChannels`

ListChannels returns all open channels.

Signature: `func (c *Client) ListChannels(ctx context.Context) ([]Channel, error)`

### `(*Client).ListPendingChannels`

ListPendingChannels returns channels that are pending open or close.

Signature: `func (c *Client) ListPendingChannels(ctx context.Context) (*PendingChannelsResponse, error)`

### `(*Client).GetInvoice`

GetInvoice returns a single invoice by its r_hash.

Signature: `func (c *Client) GetInvoice(ctx context.Context, rHash string) (*Invoice, error)`

### `(*Client).ListInvoices`

ListInvoices returns all invoices.
Set pendingOnly=true to only return unpaid invoices.

Signature: `func (c *Client) ListInvoices( ctx context.Context, pendingOnly bool, ) ([]Invoice, error)`

### `(*Client).ListPayments`

ListPayments returns all outgoing Lightning payments.
Set includeIncomplete=true to also return in-flight or failed payments.

Signature: `func (c *Client) ListPayments( ctx context.Context, includeIncomplete bool, ) ([]Payment, error)`

### `parseChannelPoint`

This function is part of the `lnd` package implementation.

Signature: `func parseChannelPoint(channelPoint string) (string, int, error)`

## Key Dependencies

- `context`
- `fmt`

