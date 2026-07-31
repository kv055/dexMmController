# `internal/wallethub/lnd_client.go`

Source package: `lnd`  
Source lines: `533`

## Package Purpose

This file contributes to the `lnd` package.

## File Role

This file defines 15 type(s), implements 22 function or method entry point(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Client`

Client is the LND REST client.

Fields:

- `host` `string`: Stores the host value for the client.
- `macaroon` `string`: Stores the macaroon value for the client.
- `httpClient` `*http.Client`: Stores the http client value for the client.

### `stateResponse`

Fields:

- `State` `string`: Stores the current status for the state response.

### `unlockRequest`

Fields:

- `WalletPassword` `string`: Stores the wallet password value for the unlock request.

### `initWalletRequest`

Fields:

- `WalletPassword` `string`: Stores the wallet password value for the init wallet request.
- `StatelessInit` `bool`: Stores the current status for the init wallet request.
- `CipherSeedMnemonic` `[]string`: Stores the cipher seed mnemonic value for the init wallet request.
- `AEZEEDPassphrase` `string`: Stores the a e z e e d passphrase value for the init wallet request.
- `RecoveryWindow` `int32`: Stores the recovery window value for the init wallet request.

### `InitConfig`

InitConfig contains optional wallet initialization material for LND.

Fields:

- `CipherSeedMnemonic` `[]string`: CipherSeedMnemonic is the cipher seed mnemonic value for the init config.
- `AEZEEDPassphrase` `string`: AEZEEDPassphrase is the aezeedpassphrase value for the init config.
- `RecoveryWindow` `int32`: RecoveryWindow is the recovery window value for the init config.

### `NewAddressResponse`

NewAddressResponse represents a new address response.

Fields:

- `Address` `string`: Address is the wallet or peer address used by the new address response.

### `OnChainBalanceResponse`

OnChainBalanceResponse contains LND wallet balance totals.

Fields:

- `TotalBalance` `string`: TotalBalance is the total balance value for the on chain balance response.
- `ConfirmedBalance` `string`: ConfirmedBalance is the confirmed balance value for the on chain balance response.
- `UnconfirmedBalance` `string`: UnconfirmedBalance is the unconfirmed balance value for the on chain balance response.

### `SendOnChainRequest`

SendOnChainRequest represents a send on chain request.

Fields:

- `Addr` `string`: Addr is the wallet or peer address used by the send on chain request.
- `Amount` `int64`: Amount is the amount value for the send on chain request.
- `SatPerVbyte` `int64`: SatPerVbyte is the sat per vbyte value for the send on chain request.

### `SendOnChainResponse`

SendOnChainResponse represents a send on chain response.

Fields:

- `Txid` `string`: Txid is the txid value for the send on chain response.

### `CreateInvoiceRequest`

CreateInvoiceRequest represents a create invoice request.

Fields:

- `ValueMsat` `int64`: ValueMsat is the amount in millisatoshis.
- `Memo` `string`: Memo carries a human-readable message for the create invoice request.
- `Expiry` `int64`: Expiry is the expiry value for the create invoice request.

### `CreateInvoiceResponse`

CreateInvoiceResponse represents a create invoice response.

Fields:

- `PaymentRequest` `string`: PaymentRequest is the payment request value for the create invoice response.
- `RHash` `string`: RHash is the rhash value for the create invoice response.

### `PayInvoiceRequest`

PayInvoiceRequest represents a pay invoice request.

Fields:

- `PaymentRequest` `string`: PaymentRequest is the payment request value for the pay invoice request.
- `FeeLimitSat` `int64`: FeeLimitSat records the relevant time for the pay invoice request.

### `PayInvoiceResponse`

PayInvoiceResponse represents a pay invoice response.

Fields:

- `PaymentError` `string`: PaymentError is the payment error value for the pay invoice response.
- `PaymentPreimage` `string`: PaymentPreimage is the payment preimage value for the pay invoice response.

### `ChannelBalanceResponse`

ChannelBalanceResponse represents a channel balance response.

Fields:

- `LocalBalance` `BalanceAmount`: LocalBalance is the local balance value for the channel balance response.
- `RemoteBalance` `BalanceAmount`: RemoteBalance is the remote balance value for the channel balance response.

### `BalanceAmount`

BalanceAmount represents a balance amount.

Fields:

- `Sat` `string`: Sat records the relevant time for the balance amount.
- `Msat` `string`: Msat is the amount in millisatoshis.

## Functions and Methods

### `New`

New initialises the LND client, waits for LND to be reachable,
handles first-boot wallet creation or unlock, then waits until
LND is fully synced and ready.

host:          e.g. "https://localhost:8080"
tlsCertPath:   path to LND's tls.cert file
macaroonPath:  path to admin.macaroon file
password:      plaintext wallet password

Signature: `func New( ctx context.Context, host, tlsCertPath, macaroonPath, password string, initCfg InitConfig, ) (*Client, error)`

### `(*Client).handleWalletState`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) handleWalletState( ctx context.Context, macaroonPath, password string, initCfg InitConfig, ) error`

### `(InitConfig).Validate`

Validate validates the provided value.

Signature: `func (cfg InitConfig) Validate() error`

### `(*Client).initWallet`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) initWallet(ctx context.Context, password string, cfg InitConfig) error`

### `(*Client).waitReachable`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) waitReachable(ctx context.Context) error`

### `(*Client).waitState`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) waitState(ctx context.Context, target string) error`

### `(*Client).getState`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) getState(ctx context.Context) (string, error)`

### `(*Client).unlockWallet`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) unlockWallet(ctx context.Context, password string) error`

### `(*Client).loadMacaroon`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) loadMacaroon(path string) error`

### `(*Client).waitMacaroon`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) waitMacaroon(ctx context.Context, path string) error`

### `(*Client).NewAddress`

NewAddress generates a new on-chain bech32 address.

Signature: `func (c *Client) NewAddress(ctx context.Context) (string, error)`

### `(*Client).OnChainBalance`

OnChainBalance returns the on-chain wallet balance.

Signature: `func (c *Client) OnChainBalance(ctx context.Context) (*OnChainBalanceResponse, error)`

### `(*Client).HealthCheck`

HealthCheck verifies that the wallet client can reach LND.

Signature: `func (c *Client) HealthCheck(ctx context.Context) error`

### `(*Client).SendOnChain`

SendOnChain sends BTC on-chain.

Signature: `func (c *Client) SendOnChain( ctx context.Context, toAddress string, amountSat, satPerVbyte int64, ) (string, error)`

### `(*Client).CreateInvoice`

CreateInvoice creates a Lightning invoice to receive a payment.

Signature: `func (c *Client) CreateInvoice( ctx context.Context, amountMsat int64, memo string, expirySeconds int64, ) (*CreateInvoiceResponse, error)`

### `(*Client).PayInvoice`

PayInvoice pays a BOLT11 Lightning invoice.

Signature: `func (c *Client) PayInvoice( ctx context.Context, paymentRequest string, feeLimitSat int64, ) (*PayInvoiceResponse, error)`

### `(*Client).ChannelBalance`

ChannelBalance returns the Lightning channel balance.

Signature: `func (c *Client) ChannelBalance(ctx context.Context) (*ChannelBalanceResponse, error)`

### `(*Client).get`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) get(ctx context.Context, path string, out any) error`

### `(*Client).post`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) post(ctx context.Context, path string, body, out any) error`

### `(*Client).delete`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) delete(ctx context.Context, path string, out any) error`

### `(*Client).do`

This function is part of the `lnd` package implementation.

Signature: `func (c *Client) do(req *http.Request, out any) error`

### `buildHTTPClient`

This function is part of the `lnd` package implementation.

Signature: `func buildHTTPClient(tlsCertPath string) (*http.Client, error)`

## Key Dependencies

- `bytes`
- `context`
- `crypto/tls`
- `crypto/x509`
- `encoding/base64`
- `encoding/hex`
- `encoding/json`
- `errors`
- `fmt`
- `io`
- `net/http`
- `os`
- `strings`
- `time`
