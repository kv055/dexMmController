# LND Wallet Secret Files

This project now uses a strict bring-your-own-wallet-secret flow for LND.

`dexMmController` will not generate a new LND wallet seed for you. If LND reports `NON_EXISTING`, the controller expects one supported wallet secret to be mounted as a file and will fail startup if none is configured.

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
- `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP`
  Optional unix timestamp for xprv/tprv restores.

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

You should also set `LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP` to the approximate unix timestamp when that wallet root key was created.

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

- `/home/kili/Dokumente/Demo_Projects/dexArbTrading/secrets/lnd_extended_master_key`
- `/home/kili/Dokumente/Demo_Projects/dexArbTrading/secrets/lnd_extended_master_key_birthday_timestamp`

Those defaults are intentionally aligned with the example bind mounts in the repo-root `docker-compose.yml`, so the files can be mounted into `mm-controller` immediately.

Useful options:

```bash
python3 scripts/generate_lnd_extended_master_key.py --network mainnet
python3 scripts/generate_lnd_extended_master_key.py --network testnet
python3 scripts/generate_lnd_extended_master_key.py --output /secure/path/lnd_xprv --birthday-output /secure/path/lnd_xprv_birthday
```

The script does not print the key by default. Add `--show` only if you explicitly want it echoed to your terminal.

## How To Mount The Files

The secret file only needs to be mounted into `mm-controller`, because `mm-controller` is the service that calls LND's `InitWallet` API.

Example bind mounts in `docker-compose.yml`:

```yaml
# mm-controller:
#   environment:
#     - LND_SEED_MNEMONIC_FILE=/run/secrets/lnd_seed_mnemonic
#     - LND_AEZEED_PASSPHRASE_FILE=/run/secrets/lnd_aezeed_passphrase
#     - LND_RECOVERY_WINDOW=250
#   volumes:
#     - lnd-data:/lnd:ro
#     - ./secrets/lnd_seed_mnemonic:/run/secrets/lnd_seed_mnemonic:ro
#     - ./secrets/lnd_aezeed_passphrase:/run/secrets/lnd_aezeed_passphrase:ro
```

Alternative extended master key setup:

```yaml
# mm-controller:
#   environment:
#     - LND_EXTENDED_MASTER_KEY_FILE=/run/secrets/lnd_extended_master_key
#     - LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP=1710000000
#   volumes:
#     - lnd-data:/lnd:ro
#     - ./secrets/lnd_extended_master_key:/run/secrets/lnd_extended_master_key:ro
```

## File Handling Advice

- Keep these files outside version control.
- Use permissions like `chmod 600`.
- Mount them read-only.
- Prefer file mounts over `.env` for wallet secrets.
- Back up the original secret separately from the machine that runs Docker.

## Notes

- This restore input is only used when the LND wallet does not already exist in its data directory.
- If `lnd-data` already contains a wallet, changing the secret file will not replace that wallet.
- To initialize a different wallet, start with a fresh LND data directory.
