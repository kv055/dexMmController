#!/usr/bin/env python3
"""
Generate a BIP32 master extended private key file for LND InitWallet.

This script creates a root extended private key (xprv/tprv) from fresh
cryptographic randomness using the BIP32 master key generation algorithm:

  I = HMAC-SHA512(key="Bitcoin seed", data=seed)
  master_private_key = I_L
  chain_code = I_R

It then serializes the master key as a Base58Check-encoded extended private key
so it can be supplied to LND's `extended_master_key` InitWallet field.
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import os
import secrets
import stat
import sys
import time
from pathlib import Path


SECP256K1_ORDER = int(
    "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16
)
MAINNET_XPRV_VERSION = bytes.fromhex("0488ADE4")
TESTNET_TPRV_VERSION = bytes.fromhex("04358394")
BASE58_ALPHABET = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
REPO_ROOT = Path(__file__).resolve().parents[2]
CONTROLLER_ROOT = REPO_ROOT / "dexMmController"
DEFAULT_OUTPUT_PATH = CONTROLLER_ROOT / "secrets" / "lnd_extended_master_key"
DEFAULT_BIRTHDAY_OUTPUT_PATH = CONTROLLER_ROOT / "secrets" / "lnd_extended_master_key_birthday_timestamp"


def sha256(data: bytes) -> bytes:
    return hashlib.sha256(data).digest()


def base58check_encode(payload: bytes) -> str:
    checksum = sha256(sha256(payload))[:4]
    raw = payload + checksum
    value = int.from_bytes(raw, "big")
    chars: list[str] = []
    while value > 0:
        value, mod = divmod(value, 58)
        chars.append(BASE58_ALPHABET[mod])
    encoded = "".join(reversed(chars)) or "1"
    leading_zeroes = len(raw) - len(raw.lstrip(b"\x00"))
    return ("1" * leading_zeroes) + encoded


def derive_master_xprv(seed: bytes, testnet: bool) -> str:
    digest = hmac.new(b"Bitcoin seed", seed, hashlib.sha512).digest()
    key_bytes = digest[:32]
    chain_code = digest[32:]
    key_int = int.from_bytes(key_bytes, "big")
    if key_int == 0 or key_int >= SECP256K1_ORDER:
        raise ValueError("generated invalid master private key; try again")

    version = TESTNET_TPRV_VERSION if testnet else MAINNET_XPRV_VERSION
    serialized = b"".join(
        [
            version,
            b"\x00",  # depth
            b"\x00\x00\x00\x00",  # parent fingerprint
            b"\x00\x00\x00\x00",  # child number
            chain_code,
            b"\x00" + key_bytes,  # private key data
        ]
    )
    return base58check_encode(serialized)


def write_secret_file(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value + "\n", encoding="utf-8")
    path.chmod(stat.S_IRUSR | stat.S_IWUSR)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a local extended master key file for LND."
    )
    parser.add_argument(
        "--network",
        choices=("mainnet", "testnet"),
        default="mainnet",
        help="Key version to generate. Defaults to mainnet (xprv).",
    )
    parser.add_argument(
        "--seed-bytes",
        type=int,
        default=32,
        help="Random seed size in bytes for BIP32 master key generation. Defaults to 32.",
    )
    parser.add_argument(
        "--output",
        default=str(DEFAULT_OUTPUT_PATH),
        help="Output path for the xprv/tprv file. Defaults to the dexMmController secrets directory.",
    )
    parser.add_argument(
        "--birthday-output",
        default=str(DEFAULT_BIRTHDAY_OUTPUT_PATH),
        help="Output path for the unix timestamp birthday file. Defaults to the dexMmController secrets directory.",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Overwrite output files if they already exist.",
    )
    parser.add_argument(
        "--show",
        action="store_true",
        help="Print the generated xprv/tprv to stdout.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if not 16 <= args.seed_bytes <= 64:
        print("--seed-bytes must be between 16 and 64", file=sys.stderr)
        return 1

    output_path = Path(args.output)
    birthday_path = Path(args.birthday_output)
    if not args.force:
        for path in (output_path, birthday_path):
            if path.exists():
                print(f"refusing to overwrite existing file: {path}", file=sys.stderr)
                return 1

    testnet = args.network == "testnet"
    birthday = str(int(time.time()))

    while True:
        seed = secrets.token_bytes(args.seed_bytes)
        try:
            xprv = derive_master_xprv(seed, testnet=testnet)
            break
        except ValueError:
            continue

    write_secret_file(output_path, xprv)
    write_secret_file(birthday_path, birthday)

    print(f"wrote extended master key to {output_path}")
    print(f"wrote birthday timestamp to {birthday_path}")
    print(f"network={args.network}")
    if args.show:
        print(xprv)
    else:
        print("key material not echoed; use --show if you really want it printed")
    print("keep both files private and mount them read-only into mm-controller")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
