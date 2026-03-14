#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
CONFIG_DIR="${CONFIG_DIR:-/etc/ecnu-ssh}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-0}"

install -d "$BIN_DIR"
install -m 0755 "$ROOT_DIR/bin/ecnu-ssh" "$BIN_DIR/ecnu-ssh"
install -m 0755 "$ROOT_DIR/bin/connect-campus-server.sh" "$BIN_DIR/connect-campus-server.sh"

install -d "$CONFIG_DIR"
install -m 0640 "$ROOT_DIR/examples/ecnu-connect-campus-server.env.example" \
  "$CONFIG_DIR/connect-campus-server.env.example"
install -m 0644 "$ROOT_DIR/examples/ecnu-ssh.env.example" \
  "$CONFIG_DIR/ecnu-ssh.env.example"
install -m 0644 "$ROOT_DIR/examples/ssh_config.example" \
  "$CONFIG_DIR/ssh_config.example"

if [[ "$INSTALL_SYSTEMD" == "1" ]]; then
  install -d "$SYSTEMD_DIR"
  install -m 0644 "$ROOT_DIR/systemd/ecnu-openconnect.service" \
    "$SYSTEMD_DIR/ecnu-openconnect.service"
  echo "Installed systemd unit to $SYSTEMD_DIR/ecnu-openconnect.service"
  echo "Run: systemctl daemon-reload"
fi

echo "Installed scripts to $BIN_DIR"
echo "Installed example configs to $CONFIG_DIR"
echo "Next steps:"
echo "  1. Copy and edit $CONFIG_DIR/connect-campus-server.env.example"
echo "  2. Optionally copy and edit $CONFIG_DIR/ecnu-ssh.env.example"
echo "  3. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config"
