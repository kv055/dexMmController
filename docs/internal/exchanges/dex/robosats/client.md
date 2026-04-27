# `internal/exchanges/dex/robosats/client.go`

Source package: `robosats`  
Source lines: `216`

## Package Purpose

This file contributes to the `robosats` package.

## File Role

This file defines 5 type(s), implements 9 function or method entry point(s), declares 1 package value(s).

## Runtime Behavior

- Handles HTTP request or response flow.
- Serializes or deserializes JSON payloads.
- Uses time windows, timestamps, or duration calculations.
- Accepts context for cancellation, deadlines, or request scoping.

## Types

### `Config`

Config represents a config.

Fields:

- `BaseURL` `string`: BaseURL is the base url value for the config.
- `AuthToken` `string`: AuthToken is the auth token value for the config.
- `TakeOfferPath` `string`: TakeOfferPath is the take offer path value for the config.
- `OpenPositionsPath` `string`: OpenPositionsPath is the open positions path value for the config.
- `PositionPath` `string`: PositionPath is the position path value for the config.
- `CreateOrderPath` `string`: CreateOrderPath is the create order path value for the config.
- `RobotPath` `string`: RobotPath is the robot path value for the config.

### `Client`

Client represents a client.

Fields:

- `baseURL` `string`: Stores an endpoint or address used by the client.
- `authToken` `string`: Stores the auth token value for the client.
- `takeOfferPath` `string`: Stores the take offer path value for the client.
- `openPositionsPath` `string`: Stores the open positions path value for the client.
- `positionPath` `string`: Stores the position path value for the client.
- `createOrderPath` `string`: Stores the create order path value for the client.
- `robotPath` `string`: Stores the robot path value for the client.
- `http` `*http.Client`: Stores the http value for the client.

### `Robot`

Robot identifies a robot.

Definition: `map[string]any`

### `Position`

Position represents a position.

Fields:

- `ID` `string`: ID identifies the position.
- `OfferID` `string`: OfferID is the offer id value for the position.
- `Type` `string`: Type is the type value for the position.
- `Status` `string`: Status is the current status of the position.
- `Amount` `json.Number`: Amount is the amount value for the position.
- `MinAmount` `json.Number`: MinAmount is the min amount value for the position.
- `MaxAmount` `json.Number`: MaxAmount is the max amount value for the position.
- `Price` `json.Number`: Price is the price value used by the position.
- `Premium` `json.Number`: Premium is the premium value for the position.
- `PaymentMethod` `string`: PaymentMethod is the payment method value for the position.
- `Raw` `json.RawMessage`: Raw preserves additional source data for the position.

### `CreatePositionParams`

CreatePositionParams represents a create position params.

Fields:

- `Type` `int`: Type is the type value for the create position params.
- `Currency` `int`: Currency names the asset or network for the create position params.
- `Amount` `int64`: Amount is the amount value for the create position params.
- `MinAmount` `int64`: MinAmount is the min amount value for the create position params.
- `MaxAmount` `int64`: MaxAmount is the max amount value for the create position params.
- `Price` `string`: Price is the price value used by the create position params.
- `Premium` `string`: Premium is the premium value for the create position params.
- `PaymentMethod` `string`: PaymentMethod is the payment method value for the create position params.

## Functions and Methods

### `NewClient`

NewClient creates a client.

Signature: `func NewClient(cfg Config) *Client`

### `(*Client).WithAuthToken`

WithAuthToken configures the RoboSats authentication token.

Signature: `func (c *Client) WithAuthToken(authToken string) *Client`

### `(*Client).GetRobot`

GetRobot retrieves a robot.

Signature: `func (c *Client) GetRobot(ctx context.Context) (Robot, error)`

### `(*Client).TakeOffer`

TakeOffer accepts an existing DEX offer.

Signature: `func (c *Client) TakeOffer(ctx context.Context, offerID string) (*Position, error)`

### `(*Client).GetOpenPositions`

GetOpenPositions retrieves an open positions.

Signature: `func (c *Client) GetOpenPositions(ctx context.Context) ([]Position, error)`

### `(*Client).GetPosition`

GetPosition retrieves a position.

Signature: `func (c *Client) GetPosition(ctx context.Context, positionID string) (*Position, error)`

### `(*Client).CreatePosition`

CreatePosition creates a position.

Signature: `func (c *Client) CreatePosition(ctx context.Context, params CreatePositionParams) (*Position, error)`

### `(*Client).request`

This function is part of the `robosats` package implementation.

Signature: `func (c *Client) request(ctx context.Context, method, path string, body any, out any) error`

### `defaultPath`

This function is part of the `robosats` package implementation.

Signature: `func defaultPath(v, fallback string) string`

## Constants and Variables

- `defaultTimeout`: No inline declaration comment is present.

## Key Dependencies

- `bytes`
- `context`
- `encoding/json`
- `errors`
- `fmt`
- `net/http`
- `strings`
- `time`

