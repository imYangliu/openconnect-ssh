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
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
CONFIG_DIR="${CONFIG_DIR:-/etc/och}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-0}"

install -d "$BIN_DIR"
install -d "$LIBEXEC_DIR"
install -m 0755 "$ROOT_DIR/och" "$BIN_DIR/och"
install -m 0755 "$ROOT_DIR/src/och.sh" "$LIBEXEC_DIR/och.sh"
install -m 0755 "$ROOT_DIR/src/och-vpn.sh" "$LIBEXEC_DIR/och-vpn.sh"
install -m 0755 "$ROOT_DIR/src/och-openconnect-keepalive.sh" "$LIBEXEC_DIR/och-openconnect-keepalive.sh"
install -m 0755 "$ROOT_DIR/src/macos-vpnc-route-wrapper.sh" "$LIBEXEC_DIR/macos-vpnc-route-wrapper.sh"
install -m 0755 "$ROOT_DIR/src/och-sudo-askpass.sh" "$LIBEXEC_DIR/och-sudo-askpass.sh"
install -m 0755 "$ROOT_DIR/src/och-vpn.sh" "$BIN_DIR/och-vpn"
install -m 0755 "$ROOT_DIR/src/och-openconnect-keepalive.sh" "$BIN_DIR/och-openconnect-keepalive"

install -d "$CONFIG_DIR"
install -m 0644 "$ROOT_DIR/.env.example" \
  "$CONFIG_DIR/.env.example"
install -m 0640 "$ROOT_DIR/examples/och-vpn.env.example" \
  "$CONFIG_DIR/och-vpn.env.example"
install -m 0644 "$ROOT_DIR/examples/och.env.example" \
  "$CONFIG_DIR/och.env.example"
install -m 0644 "$ROOT_DIR/examples/ssh_config.example" \
  "$CONFIG_DIR/ssh_config.example"

if [[ "$INSTALL_SYSTEMD" == "1" ]]; then
  install -d "$SYSTEMD_DIR"
  install -m 0644 "$ROOT_DIR/systemd/och-openconnect.service" \
    "$SYSTEMD_DIR/och-openconnect.service"
  install -m 0644 "$ROOT_DIR/systemd/och-openconnect-keepalive.service" \
    "$SYSTEMD_DIR/och-openconnect-keepalive.service"
  install -m 0644 "$ROOT_DIR/systemd/och-openconnect-keepalive.timer" \
    "$SYSTEMD_DIR/och-openconnect-keepalive.timer"
  echo "Installed systemd unit to $SYSTEMD_DIR/och-openconnect.service"
  echo "Installed keepalive units to $SYSTEMD_DIR/och-openconnect-keepalive.service and $SYSTEMD_DIR/och-openconnect-keepalive.timer"
  echo "Run: systemctl daemon-reload"
fi

echo "Installed scripts to $BIN_DIR"
echo "Installed implementation files to $LIBEXEC_DIR"
echo "Installed example configs to $CONFIG_DIR"
echo "Next steps:"
echo "  1. Copy and edit $CONFIG_DIR/.env.example as .env or ~/.config/och/och.env"
echo "  2. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config"
