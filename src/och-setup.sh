#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

load_och_config_helper() {
  local helper
  for helper in "$SCRIPT_DIR/och-config.sh" "$SCRIPT_DIR/../libexec/och/och-config.sh"; do
    if [[ -r "$helper" ]]; then
      # shellcheck disable=SC1090
      source "$helper"
      return 0
    fi
  done
  echo "Error: cannot find och-config.sh" >&2
  exit 1
}

load_och_config_helper

: "${OCH_CONFIG_FILE:=$HOME/.config/och/config.toml}"
: "${OCH_SECRETS_FILE:=$HOME/.config/och/secrets.env}"
: "${OCH_MANAGED_SSH_CONFIG:=$HOME/.ssh/och.config}"
: "${OCH_MAIN_SSH_CONFIG:=$HOME/.ssh/config}"
: "${OCH_KEYCHAIN_SERVICE:=och}"
: "${OS_NAME:=$(uname -s)}"

och_setup_is_macos() {
  [[ "$OS_NAME" == "Darwin" ]]
}

och_setup_brew_prefix() {
  if [[ -x /opt/homebrew/bin/och ]]; then
    printf '%s\n' /opt/homebrew
  elif [[ -x /usr/local/bin/och ]]; then
    printf '%s\n' /usr/local
  elif [[ -d /opt/homebrew/bin ]]; then
    printf '%s\n' /opt/homebrew
  else
    printf '%s\n' /usr/local
  fi
}

och_setup_openconnect_bin() {
  command -v openconnect 2>/dev/null || printf 'openconnect'
}

och_setup_bin_path() {
  local install_root
  install_root="$(cd "$SCRIPT_DIR/../.." && pwd)"
  printf '%s/bin/och\n' "$install_root"
}

och_setup_quote_toml() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

och_setup_valid_ipv4() {
  local ip="$1" part
  [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
  IFS=. read -r -a parts <<<"$ip"
  for part in "${parts[@]}"; do
    [[ "$part" =~ ^[0-9]+$ ]] || return 1
    (( part >= 0 && part <= 255 )) || return 1
  done
}

och_setup_valid_cidr() {
  local cidr="$1" ip prefix
  [[ "$cidr" == */* ]] || return 1
  ip="${cidr%/*}"
  prefix="${cidr#*/}"
  och_setup_valid_ipv4 "$ip" || return 1
  [[ "$prefix" =~ ^[0-9]+$ ]] || return 1
  (( prefix >= 0 && prefix <= 32 ))
}

och_setup_first_ipv4_for_host() {
  local host="$1"
  if och_setup_valid_ipv4 "$host"; then
    printf '%s\n' "$host"
    return 0
  fi

  if command -v python3 >/dev/null 2>&1; then
    python3 - "$host" <<'PY' 2>/dev/null || true
import socket
import sys

try:
    for item in socket.getaddrinfo(sys.argv[1], None, socket.AF_INET, socket.SOCK_STREAM):
        print(item[4][0])
        break
except OSError:
    pass
PY
    return 0
  fi
}

och_setup_default_cidr_for_host() {
  local ip
  ip="$(och_setup_first_ipv4_for_host "$1" | sed -n '1p')"
  [[ -n "$ip" ]] || return 0
  printf '%s/32\n' "$ip"
}

och_setup_append_route() {
  local existing="$1" route="$2" item
  [[ -n "$route" ]] || {
    printf '%s\n' "$existing"
    return 0
  }

  for item in $existing; do
    if [[ "$item" == "$route" ]]; then
      printf '%s\n' "$existing"
      return 0
    fi
  done

  if [[ -n "$existing" ]]; then
    printf '%s %s\n' "$existing" "$route"
  else
    printf '%s\n' "$route"
  fi
}

och_setup_managed_alias() {
  local host="$1"
  if [[ "$host" == och-* ]]; then
    printf '%s\n' "$host"
  else
    printf 'och-%s\n' "$host"
  fi
}

och_setup_expand_path() {
  local value="$1" base_dir="${2:-$HOME}"
  case "$value" in
    ~)
      printf '%s\n' "$HOME"
      ;;
    ~/*)
      printf '%s/%s\n' "$HOME" "${value#~/}"
      ;;
    /*)
      printf '%s\n' "$value"
      ;;
    *)
      printf '%s/%s\n' "$base_dir" "$value"
      ;;
  esac
}

och_setup_include_candidates() {
  local pattern="$1" base_dir="$2" expanded candidate
  expanded="$(och_setup_expand_path "$pattern" "$base_dir")"
  if [[ "$expanded" == *"*"* || "$expanded" == *"?"* || "$expanded" == *"["* ]]; then
    while IFS= read -r candidate; do
      [[ -r "$candidate" ]] && printf '%s\n' "$candidate"
    done < <(compgen -G "$expanded" || true)
  elif [[ -r "$expanded" ]]; then
    printf '%s\n' "$expanded"
  fi
}

och_setup_collect_ssh_hosts() {
  local file="${1:-$OCH_MAIN_SSH_CONFIG}" depth="${2:-0}"
  [[ "$depth" -lt 5 ]] || return 0
  [[ -r "$file" ]] || return 0

  local base_dir raw line keyword lower_keyword rest token include_file managed_real file_real
  base_dir="$(cd "$(dirname "$file")" && pwd)"
  managed_real="$(cd "$(dirname "$OCH_MANAGED_SSH_CONFIG")" 2>/dev/null && pwd)/$(basename "$OCH_MANAGED_SSH_CONFIG")"
  file_real="$(cd "$(dirname "$file")" 2>/dev/null && pwd)/$(basename "$file")"
  [[ "$file_real" != "$managed_real" ]] || return 0

  while IFS= read -r raw || [[ -n "$raw" ]]; do
    line="$(och_config_trim "$(och_config_strip_comment "$raw")")"
    [[ -n "$line" ]] || continue
    keyword="${line%%[[:space:]]*}"
    rest="$(och_config_trim "${line#"$keyword"}")"
    lower_keyword="$(printf '%s' "$keyword" | tr '[:upper:]' '[:lower:]')"
    case "$lower_keyword" in
      host)
        for token in $rest; do
          [[ "$token" == *"*"* || "$token" == *"?"* || "$token" == "!"* ]] && continue
          [[ "$token" == "och-"* ]] && continue
          printf '%s\n' "$token"
        done
        ;;
      include)
        for token in $rest; do
          while IFS= read -r include_file; do
            och_setup_collect_ssh_hosts "$include_file" "$((depth + 1))"
          done < <(och_setup_include_candidates "$token" "$base_dir")
        done
        ;;
    esac
  done < "$file"
}

och_setup_list_ssh_hosts() {
  och_setup_collect_ssh_hosts "$OCH_MAIN_SSH_CONFIG" | awk '!seen[$0]++'
}

och_setup_resolve_ssh_host() {
  local host="$1" resolved hostname user port
  if [[ -r "$OCH_MAIN_SSH_CONFIG" ]]; then
    resolved="$(ssh -F "$OCH_MAIN_SSH_CONFIG" -G "$host" 2>/dev/null || true)"
  else
    resolved="$(ssh -G "$host" 2>/dev/null || true)"
  fi
  hostname="$(awk '$1 == "hostname" { print $2; exit }' <<<"$resolved")"
  user="$(awk '$1 == "user" { print $2; exit }' <<<"$resolved")"
  port="$(awk '$1 == "port" { print $2; exit }' <<<"$resolved")"
  printf '%s\t%s\t%s\n' "${hostname:-$host}" "${user:-${USER:-}}" "${port:-22}"
}

och_setup_parse_authgroups() {
  sed -nE \
    -e 's/.*GROUP:[[:space:]]*\[([^]]+)\].*/\1/p' \
    -e 's/^[[:space:]]*[0-9]+[).][[:space:]]+(.+)$/\1/p' \
    -e 's/.*<option[^>]*>([^<]+)<\/option>.*/\1/p' |
    tr '|' '\n' |
    sed -E 's/^[[:space:]]+|[[:space:]]+$//g' |
    awk 'NF && !seen[$0]++'
}

och_setup_probe_authgroups() {
  local host="$1" user="$2"
  [[ -n "$host" && -n "$user" ]] || return 0
  "$(och_setup_openconnect_bin)" "$host" -u "$user" --authenticate --non-inter 2>&1 |
    och_setup_parse_authgroups || true
}

och_setup_keychain_save_password() {
  local account="$1" password="$2"
  [[ -n "$account" && -n "$password" ]] || return 0
  och_setup_is_macos || return 0
  /usr/bin/security delete-generic-password -s "$OCH_KEYCHAIN_SERVICE" -a "$account" >/dev/null 2>&1 || true
  /usr/bin/security add-generic-password -U -s "$OCH_KEYCHAIN_SERVICE" -a "$account" -w "$password" >/dev/null
}

och_setup_write_secrets_password() {
  local password="$1"
  [[ -n "$password" ]] || return 0
  och_setup_is_macos && return 0
  install -d -m 0700 "$(dirname "$OCH_SECRETS_FILE")"
  umask 077
  printf 'VPN_PASSWORD=%s\n' "$(och_setup_quote_toml "$password")" >"$OCH_SECRETS_FILE"
  chmod 600 "$OCH_SECRETS_FILE"
}

och_setup_render_toml() {
  local routes="$1" route_lines=""

  local route first=1
  for route in $routes; do
    if (( first )); then
      first=0
    else
      route_lines+=", "
    fi
    route_lines+="$(och_setup_quote_toml "$route")"
  done

  cat <<EOF
# Generated by OCH. VPN password is stored in Keychain, not in this file.

[vpn]
host = $(och_setup_quote_toml "${OCH_VPN_HOST:-}")
user = $(och_setup_quote_toml "${OCH_VPN_USER:-}")
auth_group = $(och_setup_quote_toml "${OCH_VPN_AUTHGROUP:-}")

[ssh]
host = $(och_setup_quote_toml "${OCH_SSH_HOST:-}")
target_host = $(och_setup_quote_toml "${OCH_TARGET_HOST:-}")
user = $(och_setup_quote_toml "${OCH_TARGET_SSH_USER:-${USER:-}}")
port = $(och_setup_quote_toml "${OCH_TARGET_PORT:-22}")

[routes]
extra = [$route_lines]

[proxy]
local_host = $(och_setup_quote_toml "${OCH_PROXY_LOCAL_HOST:-127.0.0.1}")
local_port = $(och_setup_quote_toml "${OCH_PROXY_LOCAL_PORT:-7890}")
remote_port = $(och_setup_quote_toml "${OCH_PROXY_REMOTE_PORT:-7890}")

[paths]
# Runtime helper paths are fixed by the installed app or CLI layout.

[app]
language = $(och_setup_quote_toml "${OCH_APP_LANGUAGE:-system}")

EOF
}

och_setup_write_config() {
  local routes="$1"
  install -d -m 0700 "$(dirname "$OCH_CONFIG_FILE")"
  och_setup_render_toml "$routes" >"$OCH_CONFIG_FILE"
  chmod 600 "$OCH_CONFIG_FILE"
}

och_setup_write_managed_ssh_config() {
  install -d -m 0700 "$(dirname "$OCH_MANAGED_SSH_CONFIG")"
cat >"$OCH_MANAGED_SSH_CONFIG" <<EOF
# Generated by OCH. Edit this file from the OCH app.
Host ${OCH_SSH_HOST}
  HostName ${OCH_TARGET_HOST}
  User ${OCH_TARGET_SSH_USER:-${USER:-}}
  Port ${OCH_TARGET_PORT:-22}
  ProxyCommand $(och_setup_bin_path) proxy-command %h %p
  ServerAliveInterval 30
  ServerAliveCountMax 3

EOF
  chmod 600 "$OCH_MANAGED_SSH_CONFIG"
}

och_setup_ensure_include_line() {
  local include_line="Include ~/.ssh/och.config"
  install -d -m 0700 "$(dirname "$OCH_MAIN_SSH_CONFIG")"
  if [[ -r "$OCH_MAIN_SSH_CONFIG" ]] && grep -Fxq "$include_line" "$OCH_MAIN_SSH_CONFIG"; then
    return 0
  fi
  if [[ -s "$OCH_MAIN_SSH_CONFIG" ]] && [[ "$(tail -c 1 "$OCH_MAIN_SSH_CONFIG")" != $'\n' ]]; then
    printf '\n' >>"$OCH_MAIN_SSH_CONFIG"
  fi
  printf '%s\n' "$include_line" >>"$OCH_MAIN_SSH_CONFIG"
  chmod 600 "$OCH_MAIN_SSH_CONFIG"
}

och_setup_prompt() {
  local label="$1" default="${2:-}" value
  if [[ -n "$default" ]]; then
    read -r -p "$label [$default]: " value
    printf '%s\n' "${value:-$default}"
  else
    read -r -p "$label: " value
    printf '%s\n' "$value"
  fi
}

och_setup_prompt_secret() {
  local label="$1" value
  read -r -s -p "$label: " value
  echo >&2
  printf '%s\n' "$value"
}

och_setup_select_authgroup() {
  local groups=("$@") choice
  [[ "${#groups[@]}" -gt 0 ]] || return 0

  echo "检测到认证组:"
  local i
  for i in "${!groups[@]}"; do
    printf '  %d) %s\n' "$((i + 1))" "${groups[$i]}"
  done
  read -r -p "选择认证组编号，或直接回车手填/留空: " choice
  if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#groups[@]} )); then
    printf '%s\n' "${groups[$((choice - 1))]}"
  fi
}

och_setup_interactive() {
  if [[ -r "$OCH_CONFIG_FILE" ]]; then
    load_och_toml_file "$OCH_CONFIG_FILE" 0
  fi

  echo "OCH setup"
  OCH_VPN_HOST="$(och_setup_prompt "VPN 网关" "${OCH_VPN_HOST:-}")"
  OCH_VPN_USER="$(och_setup_prompt "VPN 用户" "${OCH_VPN_USER:-}")"
  local vpn_password=""
  vpn_password="$(och_setup_prompt_secret "VPN 密码（macOS 保存到 Keychain，Linux 保存到 secrets.env）")"

  local -a groups=()
  if [[ -n "$OCH_VPN_HOST" && -n "$OCH_VPN_USER" ]]; then
    echo "正在尝试探测认证组（失败会跳过）..."
    local group
    while IFS= read -r group; do
      groups+=("$group")
    done < <(och_setup_probe_authgroups "$OCH_VPN_HOST" "$OCH_VPN_USER")
  fi
  OCH_VPN_AUTHGROUP="$(och_setup_select_authgroup "${groups[@]}")"
  OCH_VPN_AUTHGROUP="${OCH_VPN_AUTHGROUP:-$(och_setup_prompt "认证组（可留空）" "${OCH_VPN_AUTHGROUP:-}")}"

  echo
  echo "SSH Host:"
  local -a hosts=()
  local host
  while IFS= read -r host; do
    hosts+=("$host")
  done < <(och_setup_list_ssh_hosts)
  local i selected="" choice=""
  for i in "${!hosts[@]}"; do
    printf '  %d) %s\n' "$((i + 1))" "${hosts[$i]}"
  done
  echo "  0) 手动输入"
  read -r -p "选择要连接的 SSH Host: " choice
  if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#hosts[@]} )); then
    selected="${hosts[$((choice - 1))]}"
  fi

  if [[ -n "$selected" ]]; then
    local resolved
    resolved="$(och_setup_resolve_ssh_host "$selected")"
    IFS=$'\t' read -r OCH_TARGET_HOST OCH_TARGET_SSH_USER OCH_TARGET_PORT <<<"$resolved"
    OCH_SSH_HOST="$(och_setup_managed_alias "$selected")"
  else
    OCH_SSH_HOST="$(och_setup_prompt "托管 Host 别名" "${OCH_SSH_HOST:-och-target}")"
    OCH_TARGET_HOST="$(och_setup_prompt "HostName/IP" "${OCH_TARGET_HOST:-}")"
    OCH_TARGET_SSH_USER="$(och_setup_prompt "SSH 用户" "${OCH_TARGET_SSH_USER:-${USER:-}}")"
    OCH_TARGET_PORT="$(och_setup_prompt "SSH 端口" "${OCH_TARGET_PORT:-22}")"
  fi

  local default_cidr route_cidr routes
  default_cidr="$(och_setup_default_cidr_for_host "$OCH_TARGET_HOST")"
  route_cidr="$(och_setup_prompt "目标路由 CIDR" "$default_cidr")"
  if [[ -n "$route_cidr" ]] && ! och_setup_valid_cidr "$route_cidr"; then
    echo "Error: 无效 CIDR: $route_cidr" >&2
    exit 1
  fi
  routes="$(och_setup_append_route "${OCH_ROUTES_EXTRA:-}" "$route_cidr")"

  och_setup_write_config "$routes"
  och_setup_keychain_save_password "$OCH_VPN_USER" "$vpn_password"
  och_setup_write_secrets_password "$vpn_password"
  och_setup_write_managed_ssh_config
  och_setup_ensure_include_line

  echo "已写入配置: $OCH_CONFIG_FILE"
  echo "已写入 SSH Host: $OCH_MANAGED_SSH_CONFIG"
  echo "默认连接: ssh ${OCH_SSH_HOST}"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  och_setup_interactive "$@"
fi
