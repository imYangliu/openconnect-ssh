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
out="$(VPN_HOST=vpn.example.com VPN_USER=alice VPN_PASSWORD=secret DEFAULT_HOST=och-app TARGET_HOST=10.2.3.4 TARGET_SSH_USER=alice TARGET_PORT=22 bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_render_toml '10.2.3.4/32'")"
[[ "$out" == *'host = "vpn.example.com"'* && "$out" != *secret* ]] \
  || fail "setup rendered TOML should include config but not password: $out"
SECURITY_LOG="$TEST_TMP/security.log" OS_NAME=Darwin OCH_SECURITY_BIN="$TEST_TMP/fake-security" \
  bash -c "source '$ROOT_DIR/src/och-setup.sh'; och_setup_keychain_save_password alice secret"
assert_contains "$TEST_TMP/security.log" 'delete-generic-password -s och -a alice' \
  "setup should delete existing Keychain password before saving"
assert_contains "$TEST_TMP/security.log" 'add-generic-password -U -s och -a alice -w secret' \
  "setup should save VPN password to the macOS Keychain service"

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
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" '\[app\]' \
  "rendered config.toml should include an app section"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'language = .*appLanguage\.rawValue' \
  "rendered config.toml should persist app.language"
assert_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" 'case \("app", "language"\)' \
  "TOML parser should read app.language"
assert_not_contains "$ROOT_DIR/Sources/OCHApp/AppConfig.swift" '\[paths\]' \
  "GUI-rendered config.toml should not include legacy helper paths"
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

for strings_file in \
  "$ROOT_DIR/Sources/OCHApp/Resources/en.lproj/Localizable.strings" \
  "$ROOT_DIR/Sources/OCHApp/Resources/zh-Hans.lproj/Localizable.strings"; do
  assert_contains "$strings_file" '"pane\.connection"' \
    "$strings_file should localize pane.connection"
  assert_contains "$strings_file" '"button\.apply_toml"' \
    "$strings_file should localize button.apply_toml"
  assert_contains "$strings_file" '"log\.saved_config"' \
    "$strings_file should localize log.saved_config"
  assert_contains "$strings_file" '"error\.toml\.invalid_line"' \
    "$strings_file should localize error.toml.invalid_line"

  if command -v plutil >/dev/null 2>&1; then
    plutil -lint "$strings_file" >/dev/null
  fi
done

echo "unit tests passed"
