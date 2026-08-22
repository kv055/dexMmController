# LND Wallet Secret Files

## Fresh wallet (default)

For a **new** LND wallet you do not need to configure anything by hand. mm-controller's bootstrap supervisor runs on every startup and auto-provisions the wallet password: when `accounts.password` on the `LND wallet` account is empty, it generates a 256-bit secret, stores it encrypted in the database, and uses it for `InitWallet`. The password stays revealable via the dexMonitor Accounts panel (Account → Reveal). A password entered manually in the Accounts panel is always respected and never overwritten.

On first startup, when LND reports `NON_EXISTING`, mm-controller calls LND's `GenSeed` API, commits that aezeed with `InitWallet`, and stores the 24-word phrase in `seeds.seed_phrase`. You do not generate or paste the seed yourself.

The supervisor retries transient failures (database or LND still starting) forever. Permanent misconfigurations — missing `LND_HOST` / `LND_TLS_CERT_PATH` / `LND_MACAROON_PATH` or an invalid seed import file — stop the loop and surface through the startup diagnostics until fixed.

## Restore an existing wallet

To recover an existing LND wallet, provide its 24-word aezeed **before** first
init. This is the stack's supported wallet-root restore format.

## Restore Settings

- `LND_SEED_MNEMONIC_FILE`: one-time import file containing the 24-word LND
  aezeed mnemonic.
- `LND_AEZEED_PASSPHRASE_FILE`: optional passphrase file when the aezeed was
  created with one.
- `LND_RECOVERY_WINDOW`: optional address lookahead for aezeed restoration.

## How To Import An Existing Aezeed

If you already have an LND node, the cleanest option is to use its existing aezeed recovery phrase.

Create a file such as `./secrets/lnd_seed_mnemonic` with the 24 words on a single line:

```text
word1 word2 word3 ... word24
```

If your aezeed also uses a passphrase, create `./secrets/lnd_aezeed_passphrase` containing only that passphrase.

## How To Mount The Files

The secret file only needs to be mounted into `mm-controller`, because `mm-controller` is the service that calls LND's `InitWallet` API.

Example bind mounts in `docker-compose.yml`:

```yaml
# mm-controller:
#   env_file:
#     - ./dexMmController/.env
#   environment:
#     - LND_SEED_MNEMONIC_FILE=/run/secrets/lnd_seed_mnemonic
#     - LND_AEZEED_PASSPHRASE_FILE=/run/secrets/lnd_aezeed_passphrase
#     - LND_RECOVERY_WINDOW=250
#   volumes:
#     - lnd-data:/lnd:ro
#     - ./dexMmController/secrets/lnd_seed_mnemonic:/run/secrets/lnd_seed_mnemonic:ro
#     - ./dexMmController/secrets/lnd_aezeed_passphrase:/run/secrets/lnd_aezeed_passphrase:ro
```

## Wallet Password and Aezeed in Postgres

The LND wallet password and aezeed are stored in Postgres and visible in the dexMonitor **Accounts** panel (the password is auto-generated when the operator has not set one):

| Secret | Storage |
|--------|---------|
| Wallet password | `accounts.password` on the `LND wallet` account |
| Aezeed (24 words) | `seeds.seed_phrase` for that account |
| Static channel backups | `lnd_channel_backups.multi_chan_backup` |

`mm-controller` loads the wallet password and latest aezeed from the database before `InitWallet` / `UnlockWallet`. Do not put the wallet password in `.env` or compose env.

Mounted `LND_SEED_MNEMONIC_FILE` may still be used once to import aezeed into `seeds` when restoring an existing wallet (Bisq-style). After import, the database is canonical. Leave seeds empty for a brand-new wallet so LND can generate the aezeed.

## Lightning static channel backups (SCB)

SCB is **not** a file on disk in this stack. LND returns an encrypted backup blob via its REST API; mm-controller saves it in Postgres.

| Step | What happens |
|------|----------------|
| Schema | **unified-api** applies `dexUnifiedApi/db/schema/14_lnd_channel_backups.sql` on startup. If the table is missing in Postgres, rebuild and restart unified-api first. |
| Export | After LND wallet is ready, mm-controller exports SCB and inserts into `lnd_channel_backups`. Runs again on a periodic interval. |
| UI | dexMonitor Accounts panel → LND wallet → **Channel backups** (metadata only). |
| Recovery | After wiping `lnd-data`, keep aezeed + wallet password + SCB row in the DB. mm-controller re-inits the wallet and restores the SCB on that same startup. |

Open Lightning channels **yourself** before expecting a backup row. With zero channels, export succeeds but stores nothing useful.

## File Handling Advice

- Keep secret files outside version control.
- Use permissions like `chmod 600`.
- Mount them read-only.
- Prefer file mounts over `.env` for one-time aezeed import files.
- Back up the original secret separately from the machine that runs Docker.

## Notes

- This restore input is only used when the LND wallet does not already exist in its data directory.
- If `lnd-data` already contains a wallet, changing the secret file will not replace that wallet.
- To initialize a different wallet, start with a fresh LND data directory.
