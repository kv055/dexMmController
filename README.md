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
- `GET /api/lnd/wallet` — node, peers, channels, pending channels, and addresses (requires live LND client)
- `POST /api/lnd/addresses` — generate a new on-chain address (`{"label":"optional"}`)
- `POST /api/lnd/channels/open` — open a channel (`node_pubkey`, `peer_host`, `local_funding_amount_sat`, …)
- `POST /api/lnd/channels/close` — close a channel (`channel_point`, `force`, `sat_per_vbyte`)

`dexMonitor` calls this API through its `/api/mm-controller/*` proxy endpoints.

## Wallet Logic

The controller stores reusable wallet addresses in the `wallet_addresses` table.

- Label `Primary` is reserved for the main CEX withdrawal destination.
- LND wallet password and aezeed live in Postgres (`accounts.password`, `seeds.seed_phrase`) via the dexMonitor Accounts panel — not in `.env`.
- On first startup with an empty `seeds` row, mm-controller asks LND to generate a new aezeed (`GenSeed` + `InitWallet`) and stores it in the database.
- To restore an existing wallet, set the aezeed in Seeds (or mount `LND_SEED_MNEMONIC_FILE` once) before first init.
- The supported restore input is a 24-word aezeed mnemonic.
- Once the wallet is available, a new address is generated and stored as `Primary` when missing.
- On later startups, the stored `Primary` address is reused.

### Lightning channel backups (SCB)

- Table `lnd_channel_backups` is created by **unified-api** schema `14_lnd_channel_backups.sql` on startup (`EnsureUnifiedSchema`). Rebuild/restart unified-api before mm-controller or dexMonitor use this table.
- mm-controller exports LND static channel backups to Postgres on startup and on `LND_BACKUP_INTERVAL` (defaults to account health interval).
- After a disaster recovery wallet re-init, the latest backup is restored into LND automatically.
- Open Lightning channels manually; until channels exist there is nothing meaningful to back up.
- dexMonitor shows backup metadata (not the raw blob) on the LND wallet account.

See `LND_WALLET_SECRETS.md` for the full workflow.

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
- `ENABLE_AUTOMATED_TRADEFLOW_WORKERS` (must be `true` for DEX relay,
  recovery, and hedgeless execution/refill; leave disabled until staging validation)
- `ACCOUNT_HEALTH_INTERVAL`
- `OPENCLAW_URL`
- `HODLHODL_URL`
- `BISQ_GRPC_HOST`
- `BISQ_API_PASSWORD`
- `ROBOSATS_URL`
- `BINANCE_API_BASE_URL`
- `KRAKEN_API_BASE_URL`
- `LND_HOST`
- `LND_TLS_CERT_PATH`
- `LND_MACAROON_PATH`
- `LND_SEED_MNEMONIC_FILE`
- `LND_AEZEED_PASSPHRASE_FILE`
- `LND_RECOVERY_WINDOW`

The root Compose stack mounts LND data into the MM controller container and
reaches the LND and dashboard services over the Compose bridge network.

Exchange API keys, API secrets, and passphrases are loaded exclusively from the
shared `accounts` and `api_keys` tables. Manage them through the dexMonitor
Accounts panel; exchange credentials are not read from `.env`.

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
- Keep LND and exchange credentials out of git. LND bootstrap inputs use mounted
  secret files; exchange API credentials are encrypted in Postgres and managed
  through the dexMonitor Accounts panel.
