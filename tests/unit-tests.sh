#!/usr/bin/env bash
# Unit tests for shell helpers that are awkward to cover from the Makefile smoke
# block (route parsing branches, Homebrew path fallback). Each case sources the
# target script and calls a single function in a clean subshell, faking external
# commands (ip/route) via shell functions so no real networking is touched.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/och-unit.XXXXXX")"
trap 'rm -rf "$TEST_TMP"' EXIT

: >"$TEST_TMP/empty.env"

# Fake `ip` (Linux) and `route` (macOS) as shell functions. Defining them as
# functions shadows any real binary regardless of PATH ordering.
cat >"$TEST_TMP/fakes-linux.sh" <<'EOF'
ip() {
  if [[ "$1" == "route" && "$2" == "show" ]]; then
    echo "default via 10.0.0.1 dev eth0"
  elif [[ "$1" == "route" && "$2" == "get" ]]; then
    echo "1.2.3.4 via 10.0.0.1 dev eth0 src 10.0.0.5"
  fi
}
EOF

cat >"$TEST_TMP/fakes-macos.sh" <<'EOF'
route() {
  cat <<'OUT'
   route to: default
destination: default
    gateway: 10.0.0.1
  interface: en0
OUT
}
EOF

cat >"$TEST_TMP/config.toml" <<'EOF'
[vpn]
host = "vpn.example.com"
user = "alice"
auth_group = "staff"

[ssh]
host = "och-target"
target_host = "10.0.0.10"
user = "deploy"
port = "2222"

[routes]
extra = ["10.0.0.0/8", "192.168.0.0/16"]

[proxy]
local_host = "127.0.0.1"
local_port = "7897"
remote_port = "7890"

[paths]
och = "/opt/homebrew/bin/och"
och_vpn = "/opt/homebrew/bin/och-vpn"
askpass = "/opt/homebrew/libexec/och/och-sudo-askpass.sh"
EOF

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file="$1" pattern="$2" description="$3"
  grep -Eq "$pattern" "$file" || fail "$description"
}

assert_not_contains() {
  local file="$1" pattern="$2" description="$3"
  if grep -Eq "$pattern" "$file"; then
    fail "$description"
  fi
}

# Run `func` after sourcing och-vpn.sh with the given OS and fakes, return stdout.
run_vpn_fn() {
  local os_name="$1" fakes="$2" func="$3"
  OS_NAME="$os_name" \
  USER="${USER:-tester}" \
  OCH_CONFIG_FILE="$TEST_TMP/missing.toml" \
  ENV_FILE="$TEST_TMP/empty.env" \
    bash -c "source '$fakes'; source '$ROOT_DIR/src/och-vpn.sh'; $func"
}

# --- Linux route parsing ---
out="$(run_vpn_fn Linux "$TEST_TMP/fakes-linux.sh" 'default_route_line')"
[[ "$out" == *"default via 10.0.0.1 dev eth0"* ]] \
  || fail "Linux default_route_line returned: $out"

out="$(run_vpn_fn Linux "$TEST_TMP/fakes-linux.sh" 'route_line_for_host 1.2.3.4')"
[[ "$out" == *"dev eth0"* ]] \
  || fail "Linux route_line_for_host returned: $out"

# --- macOS route parsing ---
out="$(run_vpn_fn Darwin "$TEST_TMP/fakes-macos.sh" 'default_route_line')"
[[ "$out" == *"gateway=10.0.0.1"* && "$out" == *"interface=en0"* ]] \
  || fail "macOS default_route_line returned: $out"

out="$(run_vpn_fn Darwin "$TEST_TMP/fakes-macos.sh" 'route_line_for_host 1.2.3.4')"
[[ "$out" == *"interface=en0"* ]] \
  || fail "macOS route_line_for_host returned: $out"

# --- Homebrew vpnc-script fallback ---
out="$(bash -c "source '$ROOT_DIR/src/macos-vpnc-route-wrapper.sh'; default_vpnc_script")"
[[ "$out" == */etc/vpnc/vpnc-script ]] \
  || fail "default_vpnc_script returned: $out"

# --- TOML config parsing ---
out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/config.toml'; printf '%s|%s|%s|%s|%s|%s' \"\$VPN_HOST\" \"\$DEFAULT_HOST\" \"\$TARGET_HOST\" \"\$VPN_ROUTES\" \"\$PROXY_LOCAL_PORT\" \"\$SUDO_ASKPASS\"")"
[[ "$out" == "vpn.example.com|och-target|10.0.0.10|10.0.0.0/8 192.168.0.0/16|7897|/opt/homebrew/libexec/och/och-sudo-askpass.sh" ]] \
  || fail "TOML config parse returned: $out"

# --- TOML config bridge ---
cat >"$TEST_TMP/config.toml" <<'EOF'
[vpn]
host = "vpn.example.com"
user = "vpn-user"

[ssh]
host = "och-target"
target_host = "target.example.com"
user = "ssh-user"
port = "2222"

[routes]
extra = ["10.0.0.0/8", "192.168.0.0/16"]

[proxy]
local_host = "127.0.0.1"
local_port = "7890"
remote_port = "7891"

[paths]
och_vpn = "/tmp/och-vpn"
askpass = "/tmp/askpass"
EOF

out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/config.toml'; printf '%s|%s|%s|%s|%s|%s|%s\n' \"\$VPN_HOST\" \"\$VPN_USER\" \"\$DEFAULT_HOST\" \"\$TARGET_HOST\" \"\$TARGET_PORT\" \"\$MACOS_EXTRA_ROUTES\" \"\$CONNECT_SCRIPT\"")"
[[ "$out" == "vpn.example.com|vpn-user|och-target|target.example.com|2222|10.0.0.0/8 192.168.0.0/16|/tmp/och-vpn" ]] \
  || fail "load_och_toml_file returned: $out"

# --- SwiftUI layout regression guards ---
assert_contains "$ROOT_DIR/Sources/OCHApp/UILayout.swift" 'static let windowMinWidth' \
  "SwiftUI layout constants should define a single window minimum width"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'minWidth: UILayout\.windowMinWidth' \
  "ContentView should own the window minimum size"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/OCHApp.swift" '\.frame\(minWidth:' \
  "OCHApp should not add a second, conflicting minimum window size"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" '\bGridRow\b' \
  "settings input rows should not use GridRow; it regressed macOS text-field focus"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'allowsHitTesting\(false\)' \
  "TextEditor border overlays should not intercept clicks or text selection"

echo "unit tests passed"
