# LND Wallet Secret Files

## Fresh wallet (default)

For a **new** LND wallet you only need the **wallet password** in the dexMonitor Accounts panel (`accounts.password` on the `LND wallet` account).

On first startup, when LND reports `NON_EXISTING`, mm-controller calls LND's `GenSeed` API, commits that aezeed with `InitWallet`, and stores the 24-word phrase in `seeds.seed_phrase`. You do not generate or paste the seed yourself.

## Restore an existing wallet

To recover an existing LND wallet, provide one supported wallet secret **before** first init:

## Supported Formats

Configure exactly one of these:

- `LND_SEED_MNEMONIC_FILE`
  A file containing the 24-word LND aezeed mnemonic.
- `LND_EXTENDED_MASTER_KEY_FILE`
  A file containing an extended master private key such as `xprv` or `tprv`.

Optional companion settings:

- `LND_AEZEED_PASSPHRASE_FILE`
  Optional aezeed passphrase file if your aezeed was created with one.
- `LND_RECOVERY_WINDOW`
  Optional address lookahead for aezeed restores.
- `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP_FILE`
  Preferred file-based birthday timestamp input for xprv/tprv restores.
- `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP`
  Optional plain env var fallback for xprv/tprv restores.

## Which Format Should I Use?

Use aezeed if you are restoring an existing LND wallet.

Use an extended master key only if you already manage one intentionally and know its birthday. This is more portable across wallet ecosystems, but it is usually less convenient than aezeed for an existing LND node.

A single raw private key is not enough to initialize LND's full wallet. LND expects a wallet root secret, not one standalone key.
An extended master key is also not "the one private key used for every transaction". It is the wallet root from which LND derives many child keys and addresses.

## Are These Standard?

`aezeed` is LND-specific.
It is Lightning Labs' seed format and includes metadata such as wallet versioning and birthday.

`xprv`/`tprv` extended master keys are based on BIP32 and are broadly standard in the Bitcoin wallet ecosystem.

The fact that LND accepts an extended master key during `InitWallet` is LND behavior, but the key format itself is not unique to LND.

## How To Obtain The Files

### Option 1: Existing LND Wallet

If you already have an LND node, the cleanest option is to use its existing aezeed recovery phrase.

Create a file such as `./secrets/lnd_seed_mnemonic` with the 24 words on a single line:

```text
word1 word2 word3 ... word24
```

If your aezeed also uses a passphrase, create `./secrets/lnd_aezeed_passphrase` containing only that passphrase.

### Option 2: Existing Wallet That Can Export An Extended Master Key

If you already control a wallet that can export the master private key, create a file such as `./secrets/lnd_extended_master_key`:

```text
xprv...
```

You should also provide the approximate unix timestamp when that wallet root key was created, ideally in `./secrets/lnd_extended_master_key_birthday_timestamp`.

Important:

- Many wallets do not expose the root `xprv` at all.
- Exporting a master private key is highly sensitive.
- If you use a non-LND xprv, LND will still use its own default derivation paths. Funds on unusual/custom derivation paths may not appear automatically.

### Option 3: Generate A Fresh Extended Master Key Locally

If you intentionally want a brand-new wallet root and do not need aezeed compatibility, this repo now includes a local generator script:

```bash
cd /home/kili/Dokumente/Demo_Projects/dexArbTrading/dexMmController
python3 scripts/generate_lnd_extended_master_key.py
```

By default it writes:

- `/home/kili/Dokumente/Demo_Projects/dexArbTrading/dexMmController/secrets/lnd_extended_master_key`
- `/home/kili/Dokumente/Demo_Projects/dexArbTrading/dexMmController/secrets/lnd_extended_master_key_birthday_timestamp`

Those defaults are intentionally aligned with the `mm-controller` bind mounts in the repo-root `docker-compose.yml`, so the files can be mounted immediately.

Useful options:

```bash
python3 scripts/generate_lnd_extended_master_key.py --network mainnet
python3 scripts/generate_lnd_extended_master_key.py --network testnet
python3 scripts/generate_lnd_extended_master_key.py --output /secure/path/lnd_xprv --birthday-output /secure/path/lnd_xprv_birthday
```

The script does not print the key by default. Add `--show` only if you explicitly want it echoed to your terminal.

When `LND_EXTENDED_MASTER_KEY_FILE` is configured, `dexMmController` records a SHA-256 hash of the exact mounted file bytes in the shared database on every startup. The raw key is never stored in the database.

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

Alternative extended master key setup:

```yaml
# mm-controller:
#   env_file:
#     - ./dexMmController/.env
#   environment:
#     - LND_EXTENDED_MASTER_KEY_FILE=/run/secrets/lnd_extended_master_key
#     - LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP_FILE=/run/secrets/lnd_extended_master_key_birthday_timestamp
#   volumes:
#     - lnd-data:/lnd:ro
#     - ./dexMmController/secrets/lnd_extended_master_key:/run/secrets/lnd_extended_master_key:ro
#     - ./dexMmController/secrets/lnd_extended_master_key_birthday_timestamp:/run/secrets/lnd_extended_master_key_birthday_timestamp:ro
```

If you really want to avoid the extra file, `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP` still works as a plain env var fallback.

## Wallet Password and Aezeed in Postgres

The LND wallet password and aezeed are stored in Postgres and edited in the dexMonitor **Accounts** panel:

| Secret | Storage |
|--------|---------|
| Wallet password | `accounts.password` on the `LND wallet` account |
| Aezeed (24 words) | `seeds.seed_phrase` for that account |
| Static channel backups | `lnd_channel_backups.multi_chan_backup` |

`mm-controller` loads the wallet password and latest aezeed from the database before `InitWallet` / `UnlockWallet`. Do not put the wallet password in `.env` or compose env.

Mounted `LND_SEED_MNEMONIC_FILE` may still be used once to import aezeed into `seeds` when restoring an existing wallet (Bisq-style). After import, the database is canonical. Leave seeds empty for a brand-new wallet so LND can generate the aezeed.

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
