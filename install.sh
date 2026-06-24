#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -z "${PREFIX:-}" ]]; then
  if [[ "$(uname -s)" == "Darwin" && -d /opt/homebrew/bin ]]; then
    PREFIX="/opt/homebrew"
  else
    PREFIX="/usr/local"
  fi
fi

BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
LIBEXEC_DIR="${LIBEXEC_DIR:-$PREFIX/libexec/och}"
CONFIG_DIR="${CONFIG_DIR:-/etc/och}"

install -d "$BIN_DIR"
install -d "$LIBEXEC_DIR"
install -m 0755 "$ROOT_DIR/och" "$BIN_DIR/och"
install -m 0755 "$ROOT_DIR/src/och.sh" "$LIBEXEC_DIR/och.sh"
install -m 0755 "$ROOT_DIR/src/och-vpn.sh" "$LIBEXEC_DIR/och-vpn.sh"
install -m 0755 "$ROOT_DIR/src/macos-vpnc-route-wrapper.sh" "$LIBEXEC_DIR/macos-vpnc-route-wrapper.sh"
install -m 0755 "$ROOT_DIR/src/och-sudo-askpass.sh" "$LIBEXEC_DIR/och-sudo-askpass.sh"
install -m 0755 "$ROOT_DIR/src/och-vpn.sh" "$BIN_DIR/och-vpn"

install -d "$CONFIG_DIR"
install -m 0644 "$ROOT_DIR/.env.example" \
  "$CONFIG_DIR/.env.example"
install -m 0640 "$ROOT_DIR/examples/och-vpn.env.example" \
  "$CONFIG_DIR/och-vpn.env.example"
install -m 0644 "$ROOT_DIR/examples/och.env.example" \
  "$CONFIG_DIR/och.env.example"
install -m 0644 "$ROOT_DIR/examples/ssh_config.example" \
  "$CONFIG_DIR/ssh_config.example"

echo "Installed scripts to $BIN_DIR"
echo "Installed implementation files to $LIBEXEC_DIR"
echo "Installed example configs to $CONFIG_DIR"
echo "Next steps:"
echo "  1. Copy and edit $CONFIG_DIR/.env.example as .env or ~/.config/och/och.env"
echo "  2. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config"
