#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -x "$SCRIPT_DIR/och" ]]; then
  exec "$SCRIPT_DIR/och" vpn "$@"
fi

exec och vpn "$@"
