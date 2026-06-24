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

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# Run `func` after sourcing och-vpn.sh with the given OS and fakes, return stdout.
run_vpn_fn() {
  local os_name="$1" fakes="$2" func="$3"
  OS_NAME="$os_name" \
  USER="${USER:-tester}" \
  ENV_FILE="$TEST_TMP/empty.env" \
  CONFIG_FILE="$TEST_TMP/empty.env" \
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

echo "unit tests passed"
