# dexMmController

`dexMmController` is the market-maker controller for dexArbTrading. It records arbitrage transactions, watches premium-offer streams, manages the reusable BTC wallet address used for CEX withdrawals, performs account-health checks, and can immediately take qualifying DEX offers on supported venues.

It uses the shared Postgres schema owned by `dexUnifiedApi`.

## Current Flow

On startup the controller:

1. Connects to the shared Postgres database.
2. Reuses the `transactions` and `wallet_addresses` tables managed by `dexUnifiedApi`.
3. Ensures a reusable primary BTC address exists in `wallet_addresses`.
4. Builds external clients for LND, Hummingbot, OpenClaw, HodlHodl, Bisq, RoboSats, Binance, and Kraken where configured.
5. Runs an account-health pass for incomplete transactions and configured providers.
6. Starts a REST API on `API_ADDR`.
7. Connects to the premium-offer WebSocket stream and keeps reconnecting while the process is alive.

Inside the premium watcher:

1. Every WebSocket message is scanned for offers containing premium fields.
2. Offers with `net_premium_percent > 2.0` are collected.
3. If fewer than three qualifying offers are present, nothing is executed.
4. If three or more qualifying offers are present, one transaction row is created with status `FiatToCexInitiated`.
5. The best qualifying offer is selected by highest `net_premium_percent`.
6. The matching connector is called:
   - HodlHodl: `TakeOffer`
   - Bisq: `TakeOffer`
   - RoboSats: `TakeOffer`
7. Execution metadata or errors are written to `DexOfferCreation`.
8. On successful take, the transaction advances to `DexPositionTaken`.

If taking the offer fails, the transaction row is kept and the failure is stored for later inspection.

## Directory Layout

```text
dexMmController/
+-- main.go                         # Process wiring and startup flow
+-- internal/api/                   # REST API for monitor/UI use
+-- internal/accounthealth/         # Provider account-health checks
+-- internal/db/                    # Transaction and wallet repositories
+-- internal/healthcheck/           # Transaction follow-up healthcheck loop
+-- internal/premiumoffers/         # Premium-offer WebSocket watcher
+-- internal/wallethub/             # LND, Binance, and Kraken wallet bridges
+-- internal/hummingbot/            # Hummingbot backend client
+-- internal/openclaw/              # OpenClaw client/protocol
+-- scripts/                        # LND key helper scripts
+-- LND_WALLET_SECRETS.md           # LND restore-secret workflow
+-- Dockerfile
```

## HTTP API

Default listen address: `:5001`.

- `GET /health`
- `GET /api/transactions?limit=100&offset=0&status=DexPositionTaken&cex=Kraken&dex=Bisq&direction=BUY&incomplete_only=true`
- `GET /api/transactions/{id}`
- `GET /api/stats`
- `POST /api/accounts/health_check?provider=Bisq`

`dexMonitor` calls this API through its `/api/mm-controller/*` proxy endpoints.

## Wallet Logic

The controller stores reusable wallet addresses in the `wallet_addresses` table.

- Label `Primary` is reserved for the main CEX withdrawal destination.
- On first startup with LND configured, the controller restores the wallet from a supplied secret file instead of generating a new seed.
- Supported restore inputs are a 24-word aezeed mnemonic or an extended master key (`xprv`/`tprv`).
- A single raw private key is not enough to initialize LND's full wallet.
- If no supported LND secret is configured, startup fails instead of auto-generating one.
- Once the wallet is available, a new address is generated and stored as `Primary`.
- On later startups, the stored `Primary` address is reused.
- New transactions created through the repository automatically inherit this address when `DexBtcAddress` is empty.

See `LND_WALLET_SECRETS.md` for the full restore-secret workflow.

## Connectors

HodlHodl:

- direct REST client
- offer taking
- contract and offer liveness lookups for follow-up healthchecks

Bisq:

- direct gRPC client against the Bisq daemon
- uses `BISQ_GRPC_HOST`
- authenticates with the `password` gRPC metadata header
- supports payment accounts, offer taking, open positions, position lookup, and position creation

RoboSats:

- HTTP client with configurable endpoint paths
- supports offer taking, open positions, position lookup, and position creation

Wallet bridges:

- LND wallet restore/unlock/address generation
- Binance wallet API bridge
- Kraken wallet API bridge, including Lightning/on-chain method configuration

## Environment

Copy the example file:

```bash
cp .env.example .env
```

Important variables:

- `DATABASE_URL`
- `API_ADDR`
- `OFFER_PREMIUM_WSS_URL`
- `HEALTHCHECK_INTERVAL`
- `ACCOUNT_HEALTH_INTERVAL`
- `OPENCLAW_URL`
- `HUMMINGBOT_URL`
- `HUMMINGBOT_USERNAME`
- `HUMMINGBOT_PASSWORD`
- `HODLHODL_URL`
- `HODLHODL_API_KEY`
- `HODLHODL_API_SIGNATURE_KEY`
- `BISQ_GRPC_HOST`
- `BISQ_API_PASSWORD`
- `ROBOSATS_URL`
- `ROBOSATS_AUTH_TOKEN`
- `BINANCE_API_BASE_URL`
- `BINANCE_API_KEY`
- `BINANCE_SECRET_KEY`
- `KRAKEN_API_BASE_URL`
- `KRAKEN_API_KEY`
- `KRAKEN_SECRET_KEY`
- `LND_HOST`
- `LND_TLS_CERT_PATH`
- `LND_MACAROON_PATH`
- `LND_WALLET_PASSWORD`
- `LND_SEED_MNEMONIC_FILE`
- `LND_AEZEED_PASSPHRASE_FILE`
- `LND_RECOVERY_WINDOW`
- `LND_EXTENDED_MASTER_KEY_FILE`
- `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP_FILE`
- `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP`

The root Compose stack mounts LND data and secret files into the MM controller container and runs the service with host networking so it can reach LND and dashboard endpoints on localhost.

## Local Development

Run directly:

```bash
go run .
```

Run tests:

```bash
go test ./...
```

Build:

```bash
GOCACHE=/tmp/gocache go build ./...
```

## Database

Main tables used by this service:

- `transactions`
- `wallet_addresses`

Important transaction fields:

- `Status`: workflow state
- `DexOfferCreation`: JSON payload containing trigger metadata, qualified offers, the selected offer, and execution result or error
- `DexBtcAddress`: destination BTC address for CEX withdrawals

Shared schema files live in `dexUnifiedApi`.

## Operational Notes

- The premium watcher logs one DB transaction per qualifying batch, not one transaction per offer.
- The selected DEX offer is currently the one with the highest `net_premium_percent`.
- The controller keeps reconnecting to the WebSocket while the process is alive.
- Failed offer execution is retained in the database for inspection instead of being discarded.
- Keep LND and exchange credentials out of git. Use mounted secret files and local `.env` files.
