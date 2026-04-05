# dexMmController

Trading controller for the `dexArbTrading` stack.

It watches premium-offer websocket updates, stores trade candidates in SQLite, boots and reuses a primary wallet address for CEX withdrawals, and can immediately take the best qualifying DEX offer on HodlHodl, Bisq, or RoboSats.

## Current Flow

On startup the controller:

1. Opens SQLite and runs migrations.
2. Ensures a reusable primary BTC address exists in `wallet_addresses`.
3. Builds external clients for LND, Hummingbot, OpenClaw, HodlHodl, Bisq, and RoboSats.
4. Runs the healthcheck pass for incomplete transactions.
5. Connects to the premium-offer websocket stream and enters a reconnecting loop.

Inside the premium watcher:

1. Every websocket message is scanned for offers containing premium fields.
2. Offers with `net_premium_percent > 2.0` are collected.
3. If fewer than 3 qualifying offers are present, nothing is executed.
4. If 3 or more qualifying offers are present, one new transaction row is created with status `FiatToCexInitiated`.
5. The best qualifying offer is selected and the matching connector is called immediately:
   - HodlHodl: `TakeOffer`
   - Bisq: `TakeOffer`
   - RoboSats: `TakeOffer`
6. The execution result is written back into the same transaction row in `DexOfferCreation`.
7. On successful take, the transaction advances to `DexPositionTaken`.

If the offer take fails, the transaction row is kept and the failure is stored in `DexOfferCreation.execution_error` for later inspection.

## Wallet Logic

The controller stores reusable wallet addresses in cleartext in the `wallet_addresses` table.

- Label `Primary` is reserved for the main CEX withdrawal destination.
- On first startup with LND configured, a new address is generated and stored as `Primary`.
- On later startups, the stored `Primary` address is reused.
- New transactions created through the repository automatically inherit this address when `DexBtcAddress` is empty.

## Connectors

### HodlHodl

- Direct REST client
- Supports offer taking
- Supports reading contracts and offer liveness for follow-up healthchecks

### Bisq

- Direct RPC client against the Bisq daemon, inspired by the `dexMonitor` snapshot service
- Connects to `BISQ_GRPC_HOST`
- Authenticates with the `password` gRPC metadata header
- Supports:
  - `GetMyOffers`
  - `GetPaymentAccounts`
  - `TakeOffer`
  - `GetOpenPositions`
  - `GetPosition`
  - `CreatePosition`

### RoboSats

- HTTP client with configurable endpoint paths
- Supports:
  - `TakeOffer`
  - `GetOpenPositions`
  - `GetPosition`
  - `CreatePosition`

## Database

Main tables:

- `transactions`
- `wallet_addresses`

Important transaction fields:

- `Status`: workflow state
- `DexOfferCreation`: JSON payload containing:
  - the trigger metadata
  - all qualified offers
  - the selected offer
  - execution result or execution error
- `DexBtcAddress`: destination BTC address for CEX withdrawals

## Environment

Important variables:

- `DB_PATH`
- `OPENCLAW_URL`
- `HUMMINGBOT_URL`
- `HUMMINGBOT_USERNAME`
- `HUMMINGBOT_PASSWORD`
- `HODLHODL_URL`
- `HODLHODL_API_KEY`
- `BISQ_GRPC_HOST`
- `BISQ_API_PASSWORD`
- `ROBOSATS_URL`
- `ROBOSATS_AUTH_TOKEN`
- `OFFER_PREMIUM_WSS_URL`
- `LND_HOST`
- `LND_TLS_CERT_PATH`
- `LND_MACAROON_PATH`
- `LND_WALLET_PASSWORD`

See [.env.example](/home/kili/Dokumente/Demo_Projects/dexArbTrading/dexMmController/.env.example) for a starter file.

## Run

```bash
cd /home/kili/Dokumente/Demo_Projects/dexArbTrading/dexMmController
go mod download
go run main.go
```

## Build

```bash
env GOCACHE=/tmp/gocache go build ./...
```

## Notes

- The premium watcher logs one DB transaction per qualifying batch, not one transaction per offer.
- The selected DEX offer is currently the one with the highest `net_premium_percent`.
- The controller keeps reconnecting to the websocket as long as the process is alive.
