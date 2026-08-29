# `client.go`

## Purpose

`internal/exchangeconnectors/dex/bisq/client.go` is the Bisq v1 gRPC adapter. It
normalizes daemon offers, trades, payment accounts, payment methods, and wallet
data into controller-owned Go types so orchestration code does not depend on
generated Bisq protobuf packages.

## Runtime Role

The client connects to `BISQ_GRPC_HOST` with the configured API password. It is
used by pre-trade checks, direct Bisq wallet funding, offer taking, payment-detail
resolution, payout release, wallet-management APIs, and periodic health checks.

Instead of generated stubs, the connector maintains a minimal dynamic protobuf
descriptor in `buildFileDescriptor`. Field names, wire numbers, types, message
names, and RPC paths must exactly match the daemon schema. A field omitted from
this descriptor is silently unavailable to normalization even when the daemon
sends it.

## Function Map

### `NewClient`

```go
func NewClient(host, password string) *Client
```

Creates a lazily connected client. The first RPC initializes and reuses the gRPC
connection; connection errors are retained by the client.

### `TakeOffer`

```go
func (c *Client) TakeOffer(ctx context.Context, offerID string, params TakeOfferParams) (*Trade, error)
```

Calls `Trades/TakeOffer` with the selected payment account, fee currency, and
amount. The returned `Trade` supplies the durable trade identifier persisted by
the orchestration layer.

### `GetPosition` and `GetOpenPositions`

```go
func (c *Client) GetPosition(ctx context.Context, tradeID string) (*Trade, error)
func (c *Client) GetOpenPositions(ctx context.Context) ([]Trade, error)
```

Normalize daemon `TradeInfo` messages. Besides identifiers, amounts, phase,
state, transaction IDs, and failure details, the normalized `Trade` carries:

- `IsPaymentStartedMessageSent`: the buyer marked fiat payment as sent;
- `IsPaymentReceivedMessageSent`: the seller confirmed fiat receipt;
- `IsPayoutPublished`: the payout transaction was published;
- `ContractAsJSON`: the structured contract used to resolve counterparty
  payment details.

The three boolean fields use Bisq `TradeInfo` wire numbers 20, 21, and 22.

### `(*Trade).FiatSent`

```go
func (t *Trade) FiatSent() bool
```

Implemented in `tradephase.go`, this helper reports whether the trade reached
the buyer-fiat-sent stage. It prefers `IsPaymentStartedMessageSent`, then falls
back to known Bisq phase and state ordering. Unknown future names are not
guessed. Healthcheck uses this helper for manual escalation only; it is not
evidence that fiat reached the bank and does not authorize release.

### `(*Trade).PayoutPublished`

```go
func (t *Trade) PayoutPublished() bool
```

Reports whether Bisq reached payout publication. It prefers the daemon's
`IsPayoutPublished` boolean and falls back to known payout-or-later phase and
state names. `IsPaymentReceivedMessageSent` alone is deliberately insufficient.
Healthcheck combines this signal with a nonempty payout txid and independent
mempool/block visibility before marking a transaction complete.

### `ConfirmPaymentReceived`

```go
func (c *Client) ConfirmPaymentReceived(ctx context.Context, tradeID string) error
```

As the BTC seller, confirms receipt to the Bisq daemon so it can publish the
payout. The release layer performs a follow-up `GetPosition` to capture payout
state and transaction ID. Callers must authorize fiat arrival before invoking
this method.

### `GetCounterpartyPayment`

```go
func (c *Client) GetCounterpartyPayment(ctx context.Context, tradeID string) (json.RawMessage, error)
```

Fetches the trade contract and extracts the counterparty payment-account
payload. This is the structured Bisq alternative to chat-based payment-detail
capture.

### Wallet operations

Funding address/balance, wallet balance, transaction, send, lock/unlock,
password, and unencrypted-wallet policy methods expose the subset of Bisq
wallet management needed by direct funding and controller APIs. Money-moving
methods return daemon errors unchanged with operation context.

### `buildFileDescriptor`

```go
func buildFileDescriptor() *descriptorpb.FileDescriptorProto
```

Declares the minimal messages and services used by this connector. Descriptor
tests construct dynamic messages to catch duplicate numbers, invalid types, or
missing fields before runtime.

## Failure Behavior

RPC methods honor context cancellation and wrap connection/invocation errors
with the failed operation. Normalizers treat absent optional fields as zero
values. Protocol enum values are exposed as strings; compatibility helpers such
as `FiatSent` and `PayoutPublished` accept only names explicitly known to this
connector.
