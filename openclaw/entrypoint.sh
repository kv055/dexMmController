#!/bin/bash
set -euo pipefail

CONFIG_SRC="/config/openclaw.json"
CONFIG_DEST="$HOME/.openclaw/openclaw.json"

if [ ! -f "$CONFIG_SRC" ]; then
  echo "ERROR: No config file found at $CONFIG_SRC"
  echo "Mount your openclaw.json: -v ./openclaw.json:/config/openclaw.json"
  exit 1
fi

mkdir -p "$HOME/.openclaw"
cp "$CONFIG_SRC" "$CONFIG_DEST"

echo "==> Starting openclaw gateway"
exec openclaw gateway
