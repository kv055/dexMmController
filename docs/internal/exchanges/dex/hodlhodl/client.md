# `internal/exchanges/dex/hodlhodl/client.go`

Source package: `hodlhodl`  
Source lines: `339`

## Package Purpose

This file contributes to the `hodlhodl` package.

## File Role

This file defines 2 type(s), implements 18 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Client`

Client is a REST client for the HodlHodl API v1.
Auth uses the Authorization: Bearer <api_key> header.

Fields:

- `baseURL` `string`: Stores an endpoint or address used by the client.
- `apiKey` `string`: Stores the api key value for the client.
- `http` `*http.Client`: Stores the http value for the client.

### `MeResponse`

MeResponse represents a me response.

Fields:

- `User` `json.RawMessage`: User is the user value for the me response.

## Functions and Methods

### `NewClient`

NewClient creates a new HodlHodl API client.
baseURL should be e.g. "https://hodlhodl.com" (no trailing slash).
Pass an empty baseURL to use the default.

Signature: `func NewClient(baseURL, apiKey string) *Client`

### `(*Client).get`

get performs an authenticated GET and decodes the JSON response into out.

Signature: `func (c *Client) get(ctx context.Context, path string, out any) error`

### `(*Client).do`

This function is part of the `hodlhodl` package implementation.

Signature: `func (c *Client) do(req *http.Request, path string, out any) error`

### `(*Client).post`

post performs an authenticated POST with a JSON body and decodes the response into out.

Signature: `func (c *Client) post(ctx context.Context, path string, body any, out any) error`

### `(*Client).GetOffer`

GetOffer fetches a single offer by ID. Does not require authentication.

Signature: `func (c *Client) GetOffer(ctx context.Context, offerID string) (*Offer, error)`

### `(*Client).GetMyOffers`

GetMyOffers lists the authenticated user's own offers.

Signature: `func (c *Client) GetMyOffers(ctx context.Context) ([]Offer, error)`

### `(*Client).GetMe`

GetMe retrieves a me.

Signature: `func (c *Client) GetMe(ctx context.Context) (*MeResponse, error)`

### `(*Client).VerifySignatureKey`

VerifySignatureKey verifies that a HodlHodl API signature key is usable.

Signature: `func (c *Client) VerifySignatureKey(ctx context.Context, signatureKey string) error`

### `(*Client).SearchOffers`

SearchOffers searches the public offer book with the given filters.

Signature: `func (c *Client) SearchOffers(ctx context.Context, f OfferSearchFilters) ([]Offer, error)`

### `(*Client).GetContract`

GetContract fetches a single contract by ID. Requires authentication.

Signature: `func (c *Client) GetContract(ctx context.Context, contractID string) (*Contract, error)`

### `(*Client).GetMyContracts`

GetMyContracts lists the authenticated user's contracts, optionally filtered by status.

Signature: `func (c *Client) GetMyContracts(ctx context.Context, status ContractStatus) ([]Contract, error)`

### `(*Client).CreateContract`

CreateContract accepts an offer by creating a new contract.
Returns the newly created contract.

Signature: `func (c *Client) CreateContract(ctx context.Context, p CreateContractParams) (*Contract, error)`

### `(*Client).GetMyPaymentMethodInstructions`

GetMyPaymentMethodInstructions lists the authenticated user's saved payment
methods (bank accounts, etc.) that can be used when accepting offers.

Signature: `func (c *Client) GetMyPaymentMethodInstructions(ctx context.Context) ([]PaymentMethodInstruction, error)`

### `(*Client).TakeOffer`

TakeOffer accepts an offer by ID with a fiat amount and returns the escrow
bitcoin address to deposit BTC into. It fetches the offer version, looks up
the first payment method instruction from your account, creates the contract,
and polls until HodlHodl provisions the escrow address.

Signature: `func (c *Client) TakeOffer(ctx context.Context, offerID string, fiatAmount string) (escrowAddress string, contract *Contract, err error)`

### `(*Client).IsOfferLive`

IsOfferLive returns true if the offer exists and has status "active".

Signature: `func (c *Client) IsOfferLive(ctx context.Context, offerID string) (bool, error)`

### `(*Client).IsContractCompleted`

IsContractCompleted returns true if the contract has reached "completed" status,
meaning the BTC has been released to the buyer.

Signature: `func (c *Client) IsContractCompleted(ctx context.Context, contractID string) (bool, error)`

### `(*Client).ContractHasCounterparty`

ContractHasCounterparty returns true if the contract is past the "pending" stage,
meaning a counterparty has accepted the trade.

Signature: `func (c *Client) ContractHasCounterparty(ctx context.Context, contractID string) (bool, error)`

### `(*Client).ContractIsPaid`

ContractIsPaid returns true if the buyer has marked the contract as paid
(fiat sent), so the seller knows to check their bank account.

Signature: `func (c *Client) ContractIsPaid(ctx context.Context, contractID string) (bool, error)`

## Constants and Variables

- `defaultBaseURL`: No inline declaration comment is present.

## Key Dependencies

- `bytes`
- `context`
- `crypto/hmac`
- `crypto/sha256`
- `encoding/hex`
- `encoding/json`
- `fmt`
- `net/http`
- `strconv`
- `time`

