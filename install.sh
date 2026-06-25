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

cargo build --manifest-path "$ROOT_DIR/rust-cli/Cargo.toml" --release
install -m 0755 "$ROOT_DIR/rust-cli/target/release/och" "$BIN_DIR/och"
install -m 0755 "$ROOT_DIR/src/och-config.sh" "$LIBEXEC_DIR/och-config.sh"
install -m 0755 "$ROOT_DIR/src/och-setup.sh" "$LIBEXEC_DIR/och-setup.sh"
install -m 0755 "$ROOT_DIR/src/macos-vpnc-route-wrapper.sh" "$LIBEXEC_DIR/macos-vpnc-route-wrapper.sh"
install -m 0755 "$ROOT_DIR/src/och-sudo-askpass.sh" "$LIBEXEC_DIR/och-sudo-askpass.sh"
install -m 0755 "$ROOT_DIR/src/och-vpn-shim.sh" "$BIN_DIR/och-vpn"

install -d "$CONFIG_DIR"
install -m 0644 "$ROOT_DIR/examples/ssh_config.example" \
  "$CONFIG_DIR/ssh_config.example"

echo "Installed scripts to $BIN_DIR"
echo "Installed implementation files to $LIBEXEC_DIR"
echo "Installed example configs to $CONFIG_DIR"
echo "Next steps:"
echo "  1. On macOS, run the OCH app and save ~/.config/och/config.toml"
echo "  2. On Linux, store only VPN_PASSWORD in ~/.config/och/secrets.env with chmod 600"
echo "  3. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config"
