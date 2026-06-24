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
LIBEXEC_DIR="${LIBEXEC_DIR:-$PREFIX/libexec/ecnu-ssh}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
CONFIG_DIR="${CONFIG_DIR:-/etc/ecnu-ssh}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-0}"

install -d "$BIN_DIR"
install -d "$LIBEXEC_DIR"
install -m 0755 "$ROOT_DIR/ecnu-ssh" "$BIN_DIR/ecnu-ssh"
install -m 0755 "$ROOT_DIR/src/ecnu-ssh.sh" "$LIBEXEC_DIR/ecnu-ssh.sh"
install -m 0755 "$ROOT_DIR/src/connect-campus-server.sh" "$LIBEXEC_DIR/connect-campus-server.sh"
install -m 0755 "$ROOT_DIR/src/ecnu-openconnect-keepalive.sh" "$LIBEXEC_DIR/ecnu-openconnect-keepalive.sh"
install -m 0755 "$ROOT_DIR/src/connect-campus-server.sh" "$BIN_DIR/connect-campus-server.sh"
install -m 0755 "$ROOT_DIR/src/ecnu-openconnect-keepalive.sh" "$BIN_DIR/ecnu-openconnect-keepalive.sh"

install -d "$CONFIG_DIR"
install -m 0644 "$ROOT_DIR/.env.example" \
  "$CONFIG_DIR/.env.example"
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
  install -m 0644 "$ROOT_DIR/systemd/ecnu-openconnect-keepalive.service" \
    "$SYSTEMD_DIR/ecnu-openconnect-keepalive.service"
  install -m 0644 "$ROOT_DIR/systemd/ecnu-openconnect-keepalive.timer" \
    "$SYSTEMD_DIR/ecnu-openconnect-keepalive.timer"
  echo "Installed systemd unit to $SYSTEMD_DIR/ecnu-openconnect.service"
  echo "Installed keepalive units to $SYSTEMD_DIR/ecnu-openconnect-keepalive.service and $SYSTEMD_DIR/ecnu-openconnect-keepalive.timer"
  echo "Run: systemctl daemon-reload"
fi

echo "Installed scripts to $BIN_DIR"
echo "Installed implementation files to $LIBEXEC_DIR"
echo "Installed example configs to $CONFIG_DIR"
echo "Next steps:"
echo "  1. Copy and edit $CONFIG_DIR/.env.example as .env or ~/.config/ecnu-ssh.env"
echo "  2. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config"
