# `internal/openclaw/protocol.go`

Source package: `openclaw`  
Source lines: `41`

## Package Purpose

This file contributes to the `openclaw` package.

## File Role

This file defines 5 type(s).

## Runtime Behavior

- Serializes or deserializes JSON payloads.

## Types

### `reqMsg`

Fields:

- `Type` `string`: Stores the type value for the req msg.
- `ID` `string`: Identifies the req msg.
- `Method` `string`: Stores the method value for the req msg.
- `Params` `json.RawMessage`: Stores the params value for the req msg.

### `resMsg`

Fields:

- `Type` `string`: Stores the type value for the res msg.
- `ID` `string`: Identifies the res msg.
- `OK` `bool`: Stores the o k value for the res msg.
- `Payload` `json.RawMessage`: Stores the payload value for the res msg.
- `Error` `*errMsg`: Stores the error value for the res msg.

### `errMsg`

Fields:

- `Code` `string`: Stores the code value for the err msg.
- `Message` `string`: Stores the message value for the err msg.

### `BankTxArrivedParams`

BankTxArrivedParams represents a bank tx arrived params.

Fields:

- `Code` `string`: Code is the code value for the bank tx arrived params.

### `BankTxArrivedPayload`

BankTxArrivedPayload represents a bank tx arrived payload.

Fields:

- `Arrived` `bool`: Arrived is the arrived value for the bank tx arrived payload.

## Key Dependencies

- `encoding/json`

