#!/usr/bin/env bash
# Unit tests for shell helpers that are awkward to cover from the Makefile smoke
# block (route parsing branches, strict config and secret parsing). Each case sources the
# target script and calls a single function in a clean subshell, faking external
# commands (ip/route) via shell functions so no real networking is touched.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_TMP="$(mktemp -d "${TMPDIR:-/tmp}/och-unit.XXXXXX")"
trap 'rm -rf "$TEST_TMP"' EXIT

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
# Runtime helper paths are fixed by the installed app or CLI layout.

[app]
language = "system"
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

assert_text_not_contains() {
  local text="$1" pattern="$2" description="$3"
  if grep -Eq "$pattern" <<<"$text"; then
    fail "$description"
  fi
}

# Run `func` after sourcing och-vpn.sh with the given OS and fakes, return stdout.
run_vpn_fn() {
  local os_name="$1" fakes="$2" func="$3"
  OS_NAME="$os_name" \
  USER="${USER:-tester}" \
  OCH_CONFIG_FILE="$TEST_TMP/missing.toml" \
  OCH_SECRETS_FILE="$TEST_TMP/missing-secrets.env" \
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

# --- Shell entrypoint exposes VPN through `och vpn ...` ---
mkdir -p "$TEST_TMP/bin"
cat >"$TEST_TMP/bin/ip" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "route" && "$2" == "show" ]]; then
  echo "default via 10.0.0.1 dev eth0"
elif [[ "$1" == "route" && "$2" == "get" ]]; then
  echo "1.2.3.4 via 10.0.0.1 dev eth0 src 10.0.0.5"
fi
EOF
chmod +x "$TEST_TMP/bin/ip"

out="$(HOME="$TEST_TMP/home" OCH_CONFIG_FILE="$TEST_TMP/missing.toml" OCH_SECRETS_FILE="$TEST_TMP/missing-secrets.env" bash "$ROOT_DIR/src/och.sh" vpn help)"
[[ "$out" == *'och.sh vpn <command>'* ]] \
  || fail "shell och vpn help should render a single och entrypoint: $out"

out="$(HOME="$TEST_TMP/home" OS_NAME=Linux PATH="$TEST_TMP/bin:$PATH" OCH_CONFIG_FILE="$TEST_TMP/missing.toml" OCH_SECRETS_FILE="$TEST_TMP/missing-secrets.env" bash "$ROOT_DIR/src/och.sh" vpn status)"
[[ "$out" == *'VPN 未连接'* && "$out" == *'默认路由:'* ]] \
  || fail "shell och vpn status should dispatch through the single och entrypoint: $out"

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
printf '%s\n' '#!/usr/bin/env bash' 'echo setup' >"$package_dir/libexec/och/och-setup.sh"
printf '%s\n' '#!/usr/bin/env bash' 'echo route' >"$package_dir/libexec/och/macos-vpnc-route-wrapper.sh"
printf '%s\n' '#!/usr/bin/env bash' 'echo askpass' >"$package_dir/libexec/och/och-sudo-askpass.sh"
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
[[ -x "$prefix_dir/libexec/och/och-setup.sh" ]] \
  || fail "installer should install setup helper"
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

# --- TOML config parsing ---
out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/config.toml'; printf '%s|%s|%s|%s|%s|%s|%s|%s' \"\$OCH_VPN_HOST\" \"\$OCH_SSH_HOST\" \"\$OCH_TARGET_HOST\" \"\$OCH_ROUTES_MODE\" \"\$OCH_ROUTES_EXTRA\" \"\$OCH_PROXY_ENABLED\" \"\$OCH_PROXY_LOCAL_PORT\" \"\$OCH_APP_LANGUAGE\"")"
[[ "$out" == "vpn.example.com|och-target|10.0.0.10|extra|10.0.0.0/8 192.168.0.0/16|1|7897|system" ]] \
  || fail "TOML config parse returned: $out"

cat >"$TEST_TMP/openconnect-routes.toml" <<'EOF'
[vpn]
host = "vpn.example.com"
user = "vpn-user"

[routes]
mode = "openconnect"
extra = ["10.0.0.0/8"]
EOF
out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/openconnect-routes.toml' 0; printf '%s|%s' \"\$OCH_ROUTES_MODE\" \"\$OCH_ROUTES_EXTRA\"")"
[[ "$out" == "openconnect|10.0.0.0/8" ]] \
  || fail "routes.mode should preserve explicit openconnect mode with inactive extra routes: $out"

if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file <(printf '%s\n' '[routes]' 'mode = \"direct\"') 0" >/dev/null 2>"$TEST_TMP/bad-route-mode.err"; then
  fail "load_och_toml_file should reject invalid routes.mode"
fi
grep -q 'invalid routes.mode' "$TEST_TMP/bad-route-mode.err" \
  || fail "invalid routes.mode error was unclear: $(<"$TEST_TMP/bad-route-mode.err")"

out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file <(printf '%s\n' '[vpn]' 'host = \"vpn.example.com\"' 'user = \"vpn-user\"' '[app]' 'language = \"zh-Hant\"') 0; printf '%s|%s' \"\$OCH_APP_LANGUAGE\" \"\$OCH_PROXY_ENABLED\"")"
[[ "$out" == "zh-Hant|0" ]] \
  || fail "app.language zh-Hant and omitted [proxy] should parse cleanly: $out"

if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file <(printf '%s\n' '[app]' 'language = \"fr\"') 0" >/dev/null 2>"$TEST_TMP/bad-language.err"; then
  fail "load_och_toml_file should reject invalid app.language"
fi
grep -q 'invalid app.language' "$TEST_TMP/bad-language.err" \
  || fail "invalid app.language error was unclear: $(<"$TEST_TMP/bad-language.err")"

out="$(OS_NAME=Darwin OCH_ROUTES_MODE=openconnect OCH_ROUTES_EXTRA='10.0.0.0/8' OCH_CONFIG_FILE="$TEST_TMP/missing.toml" OCH_SECRETS_FILE="$TEST_TMP/missing-secrets.env" bash -c "source '$ROOT_DIR/src/och-vpn.sh'; resolve_vpn_script")"
[[ -z "$out" ]] \
  || fail "resolve_vpn_script should not enable wrapper in openconnect route mode: $out"
out="$(OS_NAME=Darwin OCH_ROUTES_MODE=extra OCH_ROUTES_EXTRA='10.0.0.0/8' OCH_CONFIG_FILE="$TEST_TMP/missing.toml" OCH_SECRETS_FILE="$TEST_TMP/missing-secrets.env" bash -c "source '$ROOT_DIR/src/och-vpn.sh'; resolve_vpn_script")"
[[ "$out" == *"macos-vpnc-route-wrapper.sh" ]] \
  || fail "resolve_vpn_script should enable wrapper in extra route mode: $out"

# --- Strict TOML rejects runtime path keys and unknown keys ---
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
EOF

if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/config.toml'" >/dev/null 2>"$TEST_TMP/strict.err"; then
  fail "load_och_toml_file should reject [paths] helper keys"
fi
grep -q '\[paths\] is fixed' "$TEST_TMP/strict.err" \
  || fail "strict TOML error did not mention fixed [paths]: $(<"$TEST_TMP/strict.err")"

cat >"$TEST_TMP/config.toml" <<'EOF'
[vpn]
host = "vpn.example.com"
user = "vpn-user"
unexpected = "nope"

[ssh]
host = "och-target"
target_host = "target.example.com"
EOF
if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_toml_file '$TEST_TMP/config.toml'" >/dev/null 2>"$TEST_TMP/unknown.err"; then
  fail "load_och_toml_file should reject unknown keys"
fi
grep -q 'unknown config key' "$TEST_TMP/unknown.err" \
  || fail "strict TOML error did not mention unknown key: $(<"$TEST_TMP/unknown.err")"

# --- Secret file loading ---
cat >"$TEST_TMP/secrets.env" <<'EOF'
VPN_PASSWORD="from-file"
EOF
chmod 600 "$TEST_TMP/secrets.env"
out="$(bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_secrets_file '$TEST_TMP/secrets.env'; printf '%s' \"\$VPN_PASSWORD\"")"
[[ "$out" == "from-file" ]] || fail "secret file did not load VPN_PASSWORD: $out"
cat >"$TEST_TMP/bad-secrets.env" <<'EOF'
VPN_HOST="not-allowed"
EOF
chmod 600 "$TEST_TMP/bad-secrets.env"
if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_secrets_file '$TEST_TMP/bad-secrets.env'" >/dev/null 2>"$TEST_TMP/bad-secrets.err"; then
  fail "secret loader should reject non-secret keys"
fi
grep -q 'unsupported secret key' "$TEST_TMP/bad-secrets.err" \
  || fail "secret loader error did not mention unsupported key: $(<"$TEST_TMP/bad-secrets.err")"
chmod 644 "$TEST_TMP/secrets.env"
if bash -c "source '$ROOT_DIR/src/och-config.sh'; load_och_secrets_file '$TEST_TMP/secrets.env'" >/dev/null 2>"$TEST_TMP/secret-mode.err"; then
  fail "secret loader should reject unsafe permissions"
fi
grep -q '0600 permissions' "$TEST_TMP/secret-mode.err" \
  || fail "secret loader error did not mention permissions: $(<"$TEST_TMP/secret-mode.err")"

# --- Setup helper auth group parsing ---
out="$(printf '%s\n' 'GROUP: [staff|vpn-users]' '  3) contractors' '<option>faculty</option>' | bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_parse_authgroups")"
[[ "$out" == $'staff\nvpn-users\ncontractors\nfaculty' ]] \
  || fail "och_setup_parse_authgroups returned: $out"

# --- Setup helper SSH discovery ---
mkdir -p "$TEST_TMP/home/.ssh" "$TEST_TMP/home/.ssh/conf.d"
chmod 700 "$TEST_TMP/home" "$TEST_TMP/home/.ssh"
cat >"$TEST_TMP/home/.ssh/config" <<EOF
Host app *.wild !blocked
  HostName app.internal

Include conf.d/*.conf
Include ~/.ssh/och.config
EOF
cat >"$TEST_TMP/home/.ssh/conf.d/extra.conf" <<EOF
Host db
  HostName 10.0.0.20

Host och-managed
  HostName 10.0.0.99
EOF
cat >"$TEST_TMP/home/.ssh/och.config" <<EOF
Host should-not-appear
  HostName 10.0.0.30
EOF
chmod 600 "$TEST_TMP/home/.ssh/config" "$TEST_TMP/home/.ssh/conf.d/extra.conf" "$TEST_TMP/home/.ssh/och.config"

out="$(HOME="$TEST_TMP/home" OCH_MAIN_SSH_CONFIG="$TEST_TMP/home/.ssh/config" OCH_MANAGED_SSH_CONFIG="$TEST_TMP/home/.ssh/och.config" bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_list_ssh_hosts")"
[[ "$out" == $'app\ndb' ]] \
  || fail "och_setup_list_ssh_hosts returned: $out"

# --- Setup helper ssh -G mapping ---
out="$(HOME="$TEST_TMP/home" OCH_MAIN_SSH_CONFIG="$TEST_TMP/home/.ssh/config" OCH_MANAGED_SSH_CONFIG="$TEST_TMP/home/.ssh/och.config" bash -c "source '$ROOT_DIR/src/och-setup.sh'; resolved=\$(och_setup_resolve_ssh_host app); alias=\$(och_setup_managed_alias app); printf '%s|%s\n' \"\$alias\" \"\$resolved\"")"
[[ "$out" == $'och-app|app.internal\t'"${USER:-tester}"$'\t22' ]] \
  || fail "setup ssh mapping returned: $out"

# --- Setup helper validates SSH config before writing ---
mkdir -p "$TEST_TMP/fake-ssh/bin"
cat >"$TEST_TMP/fake-ssh/bin/ssh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$SSH_LOG"
if [[ "${SSH_FAIL:-0}" == "1" ]]; then
  echo "bad ssh config" >&2
  exit 255
fi
exit 0
EOF
chmod +x "$TEST_TMP/fake-ssh/bin/ssh"

SSH_LOG="$TEST_TMP/ssh-validation.log" \
PATH="$TEST_TMP/fake-ssh/bin:$PATH" \
OCH_MANAGED_SSH_CONFIG="$TEST_TMP/generated-och.config" \
OCH_SSH_HOST=och-good \
OCH_TARGET_HOST=target.example.com \
OCH_TARGET_SSH_USER=alice \
OCH_TARGET_PORT=22 \
  bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_write_managed_ssh_config"
assert_contains "$TEST_TMP/generated-och.config" '^Host och-good$' \
  "managed SSH config should be written after ssh validation succeeds"
assert_contains "$TEST_TMP/generated-och.config" 'ProxyCommand ".*och" proxy-command %h %p' \
  "managed SSH ProxyCommand helper path should be quoted"
assert_contains "$TEST_TMP/ssh-validation.log" '.*-F .* -G och-good' \
  "managed SSH config should be validated through ssh -F <temp> -G <host>"

printf '%s\n' 'keep-existing' >"$TEST_TMP/generated-och.config"
if SSH_FAIL=1 SSH_LOG="$TEST_TMP/ssh-validation-fail.log" \
PATH="$TEST_TMP/fake-ssh/bin:$PATH" \
OCH_MANAGED_SSH_CONFIG="$TEST_TMP/generated-och.config" \
OCH_SSH_HOST=och-bad \
OCH_TARGET_HOST=target.example.com \
OCH_TARGET_SSH_USER=alice \
OCH_TARGET_PORT=22 \
  bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_write_managed_ssh_config" 2>"$TEST_TMP/ssh-validation-fail.err"; then
  fail "managed SSH config write should fail when ssh validation fails"
fi
[[ "$(cat "$TEST_TMP/generated-och.config")" == "keep-existing" ]] \
  || fail "failed SSH validation should not overwrite existing managed config"
grep -q 'generated SSH config failed validation' "$TEST_TMP/ssh-validation-fail.err" \
  || fail "SSH validation failure should explain why write was blocked"

# --- Setup helper route defaults and de-duplication ---
out="$(bash -c "source '$ROOT_DIR/src/och-setup.sh'; printf '%s|%s|%s' \"\$(och_setup_default_cidr_for_host 10.2.3.4)\" \"\$(och_setup_append_route '10.0.0.0/8' '10.2.3.4/32')\" \"\$(och_setup_append_route '10.2.3.4/32' '10.2.3.4/32')\"")"
[[ "$out" == "10.2.3.4/32|10.0.0.0/8 10.2.3.4/32|10.2.3.4/32" ]] \
  || fail "setup route helper returned: $out"
bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_valid_cidr 10.2.3.4/32; ! och_setup_valid_cidr 10.2.3.999/32; ! och_setup_valid_cidr 10.2.3.4/33" \
  || fail "setup CIDR validation failed"

# --- Setup helper config render and Keychain command path ---
cat >"$TEST_TMP/fake-security" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$SECURITY_LOG"
EOF
chmod +x "$TEST_TMP/fake-security"
out="$(OCH_VPN_HOST=vpn.example.com OCH_VPN_USER=alice VPN_PASSWORD=secret OCH_SSH_HOST=och-app OCH_TARGET_HOST=10.2.3.4 OCH_TARGET_SSH_USER=alice OCH_TARGET_PORT=22 bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_render_toml '10.2.3.4/32'")"
[[ "$out" == *'host = "vpn.example.com"'* && "$out" == *'mode = "extra"'* && "$out" != *secret* ]] \
  || fail "setup rendered TOML should include config but not password: $out"
out="$(OCH_VPN_HOST=vpn.example.com OCH_VPN_USER=alice bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_render_toml ''")"
[[ "$out" == *'mode = "openconnect"'* && "$out" == *'extra = []'* && "$out" != *'[proxy]'* ]] \
  || fail "setup rendered TOML should default to openconnect mode without routes: $out"
out="$(OCH_PROXY_ENABLED=1 OCH_PROXY_LOCAL_PORT=7897 OCH_VPN_HOST=vpn.example.com OCH_VPN_USER=alice bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_render_toml ''")"
[[ "$out" == *'[proxy]'* && "$out" == *'local_port = "7897"'* ]] \
  || fail "setup rendered TOML should include proxy only when enabled: $out"
for os_name in Linux Darwin; do
  generated_secret="$TEST_TMP/generated-${os_name}-secrets.env"
  OS_NAME="$os_name" OCH_SECRETS_FILE="$generated_secret" \
    bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_write_secrets_password secret"
  [[ "$(stat -c '%a' "$generated_secret" 2>/dev/null || stat -f '%Lp' "$generated_secret")" == "600" ]] \
    || fail "$os_name generated secrets.env should have 0600 permissions"
  assert_contains "$generated_secret" '^VPN_PASSWORD="secret"$' \
    "$os_name setup should save VPN_PASSWORD to secrets.env"
done

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
assert_not_contains "$ROOT_DIR/Makefile" 'run-gui-foreground' \
  "Makefile should not keep a second foreground GUI launch target"
assert_contains "$ROOT_DIR/Makefile" 'OCHApp\.app' \
  "make run-gui should launch a temporary macOS app bundle"
assert_contains "$ROOT_DIR/Makefile" 'open -n' \
  "make run-gui should use LaunchServices instead of foreground execution"
assert_not_contains "$ROOT_DIR/Makefile" 'nohup \.build/debug/OCHApp' \
  "make run-gui should not background a bare SwiftPM executable"

# --- Single public och entrypoint guards ---
assert_contains "$ROOT_DIR/src/och.sh" 'run_vpn_command' \
  "shell entrypoint should dispatch VPN operations via och vpn"
assert_not_contains "$ROOT_DIR/install.sh" 'och-vpn-shim|BIN_DIR/och-vpn|/och-vpn' \
  "installer should not publish a separate och-vpn command"
assert_not_contains "$ROOT_DIR/Makefile" 'och-vpn-shim|bin/och-vpn' \
  "Makefile should not package or check the removed och-vpn shim"
assert_not_contains "$ROOT_DIR/install.sh" 'cargo|rustc|build-essential|rust-cli/Cargo.toml' \
  "release installer should not require Rust or local source builds"
assert_contains "$ROOT_DIR/Makefile" 'cargo build --manifest-path \$\(RUST_CLI_MANIFEST\) --release' \
  "make install should keep the developer source-build install path"
assert_contains "$ROOT_DIR/.github/workflows/release.yml" 'och-cli-\$\{version\}-darwin-arm64' \
  "release workflow should publish the macOS CLI artifact"
assert_contains "$ROOT_DIR/.github/workflows/release.yml" 'och-cli-\$\{version\}-linux-x86_64' \
  "release workflow should publish the Linux CLI artifact"
assert_contains "$ROOT_DIR/.github/workflows/release.yml" 'OCHApp-\$\{version\}-darwin-arm64\.zip' \
  "release workflow should publish the macOS app artifact"
assert_contains "$ROOT_DIR/.github/workflows/release.yml" 'macos-26' \
  "release workflow should build macOS artifacts on an arm64 runner"
assert_contains "$ROOT_DIR/rust-cli/src/vpn.rs" 'sudo_cached_credentials_available' \
  "Rust CLI should check cached sudo credentials before askpass fallback"
assert_contains "$ROOT_DIR/rust-cli/src/vpn.rs" 'SudoMode::AskpassFallback' \
  "Rust CLI should keep askpass as a fallback sudo mode"
assert_contains "$ROOT_DIR/src/och-vpn.sh" 'sudo -n true' \
  "shell VPN helper should try cached sudo credentials before askpass fallback"
assert_contains "$ROOT_DIR/src/och-sudo-askpass.sh" 'Administrator password for OCH' \
  "askpass helper should clearly ask for the administrator password"
assert_not_contains "$ROOT_DIR/README.md" '由 .*sudo -A.* 触发|默认.*sudo -A' \
  "README should not say the GUI defaults to sudo -A"
assert_not_contains "$ROOT_DIR/docs/usage.md" '由 .*sudo -A.* 触发|默认.*sudo -A' \
  "usage docs should not say the GUI defaults to sudo -A"
assert_contains "$ROOT_DIR/README.md" 'sudo -v' \
  "README should explain how to avoid the askpass fallback"
assert_contains "$ROOT_DIR/docs/usage.md" 'sudo -v' \
  "usage docs should explain how to avoid the askpass fallback"
assert_not_contains "$ROOT_DIR/README.md" 'och-vpn' \
  "README should not document a public och-vpn command"
assert_not_contains "$ROOT_DIR/docs/usage.md" 'och-vpn' \
  "usage docs should not document a public och-vpn command"
assert_not_contains "$ROOT_DIR/docs/configuration.md" 'och-vpn' \
  "configuration docs should not document a public och-vpn command"
assert_not_contains "$ROOT_DIR/CONTRIBUTING.md" 'och-vpn' \
  "contributing docs should not document a public och-vpn command"

# --- SwiftUI localization guards ---
assert_contains "$ROOT_DIR/Package.swift" 'defaultLocalization: "en"' \
  "SwiftPM package should declare English as the default localization"
assert_contains "$ROOT_DIR/Package.swift" '\.process\("Resources"\)' \
  "SwiftPM target should bundle localization resources"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" '@AppStorage\(AppLanguage\.storageKey\)' \
  "language preference should come from config.toml, not UserDefaults"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/Localization.swift" '\bUserDefaults\b' \
  "localization helper should not persist language outside config.toml"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'Label\("button\.' \
  "SwiftUI labels should render translated strings from Bundle.module, not localization keys"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'Text\("label\.' \
  "SwiftUI text should render translated strings from Bundle.module, not localization keys"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'FormSection\("section\.' \
  "form sections should render translated strings from Bundle.module, not localization keys"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'var appLanguage: AppLanguage = \.system' \
  "AppConfig should carry the GUI language preference"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'var routeMode: AppRouteMode = \.openconnect' \
  "AppConfig should default to OpenConnect route mode"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'var proxyEnabled = false' \
  "AppConfig should default proxy settings to disabled"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'mode = .*routeMode\.rawValue' \
  "rendered config.toml should persist routes.mode"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'Picker\(tr\("field\.route_mode"\)' \
  "Routes pane should expose a route mode picker"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" '\.disabled\(model\.config\.routeMode == \.openconnect\)' \
  "Extra routes editor should be inactive in OpenConnect route mode"
# shellcheck disable=SC2016
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'Toggle\(isOn: \$model\.config\.proxyEnabled\)' \
  "Routes pane should expose a proxy enable toggle"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" '\.disabled\(!model\.config\.proxyEnabled\)' \
  "Proxy fields should be disabled while proxy is off"
assert_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'error: model\.config\.proxyEnabled' \
  "Proxy validation should only run while proxy is enabled"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/SetupWizardView.swift" 'routeCIDR = SetupCIDRHelper\.defaultCIDR|State\(initialValue: SetupCIDRHelper\.defaultCIDR' \
  "Setup wizard should not default target /32 routes anymore"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" '\[app\]' \
  "rendered config.toml should include an app section"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'language = .*appLanguage\.rawValue' \
  "rendered config.toml should persist app.language"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'case \("app", "language"\)' \
  "TOML parser should read app.language"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'config\.proxyEnabled = true' \
  "TOML parser should enable proxy when a [proxy] section is present"
assert_contains "$ROOT_DIR/Sources/OCHApp/Localization.swift" 'case zhHant = "zh-Hant"' \
  "Swift localization should support Traditional Chinese"
assert_contains "$ROOT_DIR/Sources/OCHApp/Localization.swift" 'code\.lowercased\(\)' \
  "Swift localization should tolerate SwiftPM lower-cased .lproj paths"
required_keys_block="$(awk '/private static let requiredKeys = \[/,/\]/' "$ROOT_DIR/Sources/OCHApp/AppConfig.swift")"
assert_text_not_contains "$required_keys_block" 'ssh\.target_host|ssh\.host' \
  "GUI TOML required keys should allow VPN-only setup without SSH fields"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'var hasManagedSSHConfig: Bool' \
  "AppConfig should centralize complete managed SSH detection"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppModel.swift" 'if config\.hasManagedSSHConfig \{' \
  "GUI save/setup should only write managed SSH config when SSH fields are complete"
assert_contains "$ROOT_DIR/Sources/OCHApp/SSHConfigManager.swift" 'validateSSHConfig\(contents: contents, host: config\.defaultHost\)' \
  "GUI should validate generated managed SSH config before writing it"
assert_contains "$ROOT_DIR/Sources/OCHApp/SSHConfigManager.swift" 'arguments: \["-F", tempConfig\.path, "-G", host\]' \
  "GUI SSH validation should delegate parsing to OpenSSH"
assert_contains "$ROOT_DIR/Sources/OCHApp/SSHConfigManager.swift" 'quoteSSHConfigValue\(ochPath\)' \
  "GUI managed SSH ProxyCommand should quote helper paths"
# shellcheck disable=SC2016
assert_contains "$ROOT_DIR/src/och-setup.sh" 'och_setup_validate_ssh_config "\$tmp_config" "\$OCH_SSH_HOST"' \
  "setup helper should validate managed SSH config before writing it"
assert_contains "$ROOT_DIR/Sources/OCHApp/SetupWizardView.swift" 'button\.skip_ssh' \
  "Setup wizard should expose a Skip SSH action on the VPN step"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" '\[paths\]' \
  "GUI-rendered config.toml should preserve the empty paths section"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'och_vpn =|askpass =|och = .*config' \
  "GUI-rendered config.toml should not include editable helper path keys"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/ContentView.swift" 'section\.paths' \
  "GUI should not expose editable legacy helper paths"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/AppModel.swift" 'helper_path_fallback' \
  "GUI should not log configured-helper fallback messages"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppModel.swift" '"VPN_PASSWORD"' \
  "GUI connect should pass the current password through VPN_PASSWORD"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/HelperPaths.swift" 'configuredPath|repositoryCandidates|homebrewRelativePath' \
  "GUI helper resolver should ignore configured, repository, and Homebrew paths"
assert_contains "$ROOT_DIR/Sources/OCHApp/HelperPaths.swift" 'Contents/Resources/' \
  "GUI helper resolver should target app bundle resources"
assert_not_contains "$ROOT_DIR/src/och-config.sh" 'ENV_FILE|PROJECT_ENV_FILE|CONNECT_SCRIPT|VPN_ROUTES|MACOS_EXTRA_ROUTES|VPN_SCRIPT_CMD|OCH_PATH|OCH_VPN_PATH|OCH_ASKPASS_PATH' \
  "shell config loader should not keep old config override or helper-path bridge names"
assert_not_contains "$ROOT_DIR/src/och-vpn.sh" 'vpn-slice|uvx|VPN_ROUTES|MACOS_EXTRA_ROUTES|VPN_SCRIPT_CMD|ENV_FILE|PROJECT_ENV_FILE' \
  "VPN script should not keep old route-script fallback or env override names"

for strings_file in \
  "$ROOT_DIR/Sources/OCHApp/Resources/en.lproj/Localizable.strings" \
  "$ROOT_DIR/Sources/OCHApp/Resources/zh-Hans.lproj/Localizable.strings" \
  "$ROOT_DIR/Sources/OCHApp/Resources/zh-Hant.lproj/Localizable.strings"; do
  assert_contains "$strings_file" '"pane\.connection"' \
    "$strings_file should localize pane.connection"
  assert_contains "$strings_file" '"language\.zh_hant"' \
    "$strings_file should localize language.zh_hant"
  assert_contains "$strings_file" '"toggle\.enable_proxy"' \
    "$strings_file should localize toggle.enable_proxy"
  assert_contains "$strings_file" '"button\.apply_toml"' \
    "$strings_file should localize button.apply_toml"
  assert_contains "$strings_file" '"button\.skip_ssh"' \
    "$strings_file should localize button.skip_ssh"
  assert_contains "$strings_file" '"log\.saved_config"' \
    "$strings_file should localize log.saved_config"
  assert_contains "$strings_file" '"error\.toml\.invalid_line"' \
    "$strings_file should localize error.toml.invalid_line"
  assert_contains "$strings_file" '"error\.toml\.paths_key"' \
    "$strings_file should localize error.toml.paths_key"

  if command -v plutil >/dev/null 2>&1; then
    plutil -lint "$strings_file" >/dev/null
  fi
done

echo "unit tests passed"
