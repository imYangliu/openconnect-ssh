#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/och-unit.XXXXXX")"
trap 'rm -rf "$TEST_TMP"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# --- Release installer platform and artifact naming ---
out="$(OCH_INSTALL_LIBRARY_MODE=1 bash -c "source '$ROOT_DIR/install.sh'; OS_NAME=Darwin; ARCH_NAME=arm64; version=v1.2.3; platform=\$(platform_key); printf '%s|%s' \"\$platform\" \"\$(artifact_name \"\$version\" \"\$platform\")\"")"
[[ "$out" == "darwin-arm64|och-cli-v1.2.3-darwin-arm64.tar.gz" ]] \
  || fail "installer should resolve macOS arm64 artifact name: $out"

out="$(OCH_INSTALL_LIBRARY_MODE=1 bash -c "source '$ROOT_DIR/install.sh'; OS_NAME=Linux; ARCH_NAME=x86_64; version=v1.2.3; platform=\$(platform_key); printf '%s|%s' \"\$platform\" \"\$(artifact_name \"\$version\" \"\$platform\")\"")"
[[ "$out" == "linux-x86_64|och-cli-v1.2.3-linux-x86_64.tar.gz" ]] \
  || fail "installer should resolve Linux x86_64 artifact name: $out"

if OCH_INSTALL_LIBRARY_MODE=1 bash -c "source '$ROOT_DIR/install.sh'; OS_NAME=Linux; ARCH_NAME=arm64; platform_key" >/dev/null 2>"$TEST_TMP/unsupported-platform.err"; then
  fail "installer should reject unsupported Linux arm64"
fi
grep -q 'macOS arm64 和 Linux x86_64' "$TEST_TMP/unsupported-platform.err" \
  || fail "unsupported platform error should name supported platforms: $(<"$TEST_TMP/unsupported-platform.err")"

# --- Release installer integration with local asset ---
release_dir="$TEST_TMP/release"
package_dir="$TEST_TMP/package"
prefix_dir="$TEST_TMP/prefix"
config_dir="$TEST_TMP/etc/och"
mkdir -p "$release_dir" "$package_dir/bin" "$package_dir/libexec/och" "$package_dir/examples"
printf '%s\n' '#!/usr/bin/env bash' 'echo och-test' >"$package_dir/bin/och"
printf '%s\n' '#!/usr/bin/env bash' 'echo route' >"$package_dir/libexec/och/macos-vpnc-route-wrapper.sh"
printf '%s\n' 'Host och-target' >"$package_dir/examples/ssh_config.example"
chmod +x "$package_dir/bin/och" "$package_dir/libexec/och/"*.sh
tar -czf "$release_dir/och-cli-vtest-linux-x86_64.tar.gz" -C "$package_dir" .

OCH_OS_NAME=Linux \
OCH_ARCH=x86_64 \
OCH_OS_ID=debian \
OCH_VERSION=vtest \
OCH_RELEASE_BASE_URL="file://$release_dir" \
PREFIX="$prefix_dir" \
CONFIG_DIR="$config_dir" \
  bash "$ROOT_DIR/install.sh" --no-deps >/dev/null

[[ -x "$prefix_dir/bin/och" ]] \
  || fail "installer should install release binary"
[[ -x "$prefix_dir/libexec/och/macos-vpnc-route-wrapper.sh" ]] \
  || fail "installer should install route wrapper"
[[ -f "$config_dir/ssh_config.example" ]] \
  || fail "installer should install example ssh config"

# --- vpnc-script missing path is an explicit failure ---
if bash -c "source '$ROOT_DIR/src/macos-vpnc-route-wrapper.sh'; VPNC_SCRIPT_BASE='$TEST_TMP/missing-vpnc-script'; run_base_script" >/dev/null 2>"$TEST_TMP/vpnc.err"; then
  fail "run_base_script should fail when VPNC_SCRIPT_BASE is missing"
fi
grep -q 'missing executable vpnc-script' "$TEST_TMP/vpnc.err" \
  || fail "missing vpnc-script error was unclear: $(<"$TEST_TMP/vpnc.err")"

cat >"$TEST_TMP/base-vpnc-script" <<'EOF'
#!/usr/bin/env bash
printf '%s|%s|%s\n' "${INTERNAL_IP4_DNS-unset}" "${CISCO_DEF_DOMAIN-unset}" "${CISCO_SPLIT_DNS-unset}"
EOF
chmod +x "$TEST_TMP/base-vpnc-script"
out="$(INTERNAL_IP4_DNS=10.0.0.53 CISCO_DEF_DOMAIN=vpn.example.com CISCO_SPLIT_DNS=corp.example.com bash -c "source '$ROOT_DIR/src/macos-vpnc-route-wrapper.sh'; VPNC_SCRIPT_BASE='$TEST_TMP/base-vpnc-script'; OCH_DNS_MODE=ignore; run_base_script")"
[[ "$out" == "unset|unset|unset" ]] \
  || fail "DNS ignore mode should strip pushed DNS environment before vpnc-script: $out"
out="$(INTERNAL_IP4_DNS=10.0.0.53 CISCO_DEF_DOMAIN=vpn.example.com CISCO_SPLIT_DNS=corp.example.com bash -c "source '$ROOT_DIR/src/macos-vpnc-route-wrapper.sh'; VPNC_SCRIPT_BASE='$TEST_TMP/base-vpnc-script'; OCH_DNS_MODE=openconnect; run_base_script")"
[[ "$out" == "10.0.0.53|vpn.example.com|corp.example.com" ]] \
  || fail "OpenConnect DNS mode should preserve DNS environment for vpnc-script: $out"
