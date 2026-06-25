#!/usr/bin/env bash
set -euo pipefail

PATH="/sbin:/usr/sbin:$PATH"
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

: "${PID_FILE:=/tmp/och-openconnect-${USER}.pid}"
: "${LOG_FILE:=/tmp/och-openconnect-${USER}.log}"
: "${OCH_CONFIG_FILE:=$HOME/.config/och/config.toml}"
: "${OCH_SECRETS_FILE:=$HOME/.config/och/secrets.env}"
: "${OCH_KEYCHAIN_SERVICE:=och}"
: "${OS_NAME:=$(uname -s)}"
OCH_COMMAND_NAME="${OCH_COMMAND_NAME:-$(basename "$0")}"

load_och_config_helper

if [[ -r "$OCH_CONFIG_FILE" ]]; then
  load_och_toml_file "$OCH_CONFIG_FILE"
fi
load_och_secrets_file "$OCH_SECRETS_FILE"

usage() {
  cat <<EOF
OCH AnyConnect / OpenConnect 单机连接脚本

用法:
  ${OCH_COMMAND_NAME} <command>

命令:
  connect      连接 VPN
  disconnect   断开 VPN
  status       显示当前连接状态
  verify       验证目标路由和目标端口连通性
  ssh          通过 VPN SSH 到配置的目标主机
  logs         查看最近日志
  help         显示帮助

环境变量:
  OCH_CONFIG_FILE   OCH TOML 配置文件，默认 ${OCH_CONFIG_FILE}
  OCH_SECRETS_FILE  只含 VPN_PASSWORD 的 secret 文件，默认 ${OCH_SECRETS_FILE}
  VPN_PASSWORD      可选；优先于 secret 文件和 macOS Keychain fallback
  SUDO_ASKPASS      可选；sudo 无缓存时的管理员密码 fallback
  PID_FILE          PID 文件路径，默认 ${PID_FILE}
  LOG_FILE          日志文件路径，默认 ${LOG_FILE}

示例:
  ${OCH_COMMAND_NAME} connect
  ${OCH_COMMAND_NAME} verify
  ${OCH_COMMAND_NAME} ssh
  ${OCH_COMMAND_NAME} disconnect
EOF
}

error() {
  echo "Error: $*" >&2
  exit 1
}

require_tool() {
  local tool="$1"

  if [[ "$tool" == */* ]]; then
    [[ -x "$tool" ]] || error "缺少可执行文件: $tool"
    return 0
  fi

  command -v "$tool" >/dev/null 2>&1 || error "缺少依赖命令: $tool"
}

require_value() {
  local var_name="$1"
  local hint="$2"

  [[ -n "${!var_name:-}" ]] || error "$hint"
}

sudo_cmd() {
  if sudo -n true >/dev/null 2>&1; then
    sudo "$@"
  elif [[ -n "${SUDO_ASKPASS:-}" ]]; then
    sudo -A "$@"
  else
    echo 'Error: sudo 需要管理员授权。请先在终端运行 sudo -v，或设置 SUDO_ASKPASS 作为 GUI fallback。' >&2
    return 1
  fi
}

is_macos() {
  [[ "$OS_NAME" == "Darwin" ]]
}

openconnect_bin() {
  command -v openconnect 2>/dev/null || printf 'openconnect'
}

target_host() {
  printf '%s' "${OCH_RUNTIME_TARGET_HOST:-${OCH_TARGET_HOST:-}}"
}

target_port() {
  printf '%s' "${OCH_RUNTIME_TARGET_PORT:-${OCH_TARGET_PORT:-22}}"
}

target_user() {
  printf '%s' "${OCH_RUNTIME_TARGET_USER:-${OCH_TARGET_SSH_USER:-${USER:-}}}"
}

resolve_vpn_script() {
  if is_macos && [[ "${OCH_ROUTES_MODE:-openconnect}" == "extra" && -n "${OCH_ROUTES_EXTRA:-}" ]]; then
    printf '%s' "$SCRIPT_DIR/macos-vpnc-route-wrapper.sh"
  fi
}

read_vpn_password() {
  if [[ -n "${VPN_PASSWORD:-}" ]]; then
    printf '%s' "$VPN_PASSWORD"
    return 0
  fi

  if is_macos && [[ -n "${OCH_VPN_USER:-}" && -x /usr/bin/security ]]; then
    local keychain_password
    keychain_password="$(/usr/bin/security find-generic-password \
      -s "$OCH_KEYCHAIN_SERVICE" \
      -a "$OCH_VPN_USER" \
      -w 2>/dev/null || true)"
    if [[ -n "$keychain_password" ]]; then
      printf '%s' "$keychain_password"
      return 0
    fi
  fi

  local vpn_password
  read -r -s -p 'VPN password: ' vpn_password
  echo
  printf '%s' "$vpn_password"
}

is_connected() {
  [[ -s "$PID_FILE" ]] || return 1

  local pid
  pid=$(<"$PID_FILE")
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  ps -p "$pid" -o comm= 2>/dev/null | grep -qx 'openconnect'
}

default_route_line() {
  if is_macos; then
    route -n get default 2>/dev/null | awk -F: '
      $1 ~ /gateway|interface/ {
        key=$1
        value=$2
        gsub(/^[ \t]+|[ \t]+$/, "", key)
        gsub(/^[ \t]+|[ \t]+$/, "", value)
        printf "%s=%s ", key, value
      }
      END { print "" }
    '
    return 0
  fi

  ip route show default | sed -n '1p'
}

route_line_for_host() {
  local host="$1"

  if is_macos; then
    route -n get "$host" 2>/dev/null | awk -F: '
      $1 ~ /route to|destination|gateway|interface/ {
        key=$1
        value=$2
        gsub(/^[ \t]+|[ \t]+$/, "", key)
        gsub(/^[ \t]+|[ \t]+$/, "", value)
        printf "%s=%s ", key, value
      }
      END { print "" }
    '
    return 0
  fi

  ip route get "$host" | sed -n '1p'
}

default_route_iface() {
  if is_macos; then
    route -n get default 2>/dev/null | awk '$1 == "interface:" {print $2; exit}'
    return 0
  fi

  ip route show default | awk '/default/ {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}'
}

route_iface_for_host() {
  local host="$1"

  if is_macos; then
    route -n get "$host" 2>/dev/null | awk '$1 == "interface:" {print $2; exit}'
    return 0
  fi

  ip route get "$host" | awk '{for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}'
}

wait_for_target_route() {
  local timeout_seconds="${1:-15}"
  local host target_iface="" attempt

  host="$(target_host)"
  [[ -n "$host" ]] || return 0

  for ((attempt=0; attempt<timeout_seconds; attempt++)); do
    target_iface=$(route_iface_for_host "$host" || true)
    if [[ -n "$target_iface" ]]; then
      return 0
    fi
    sleep 1
  done

  return 1
}

show_status() {
  local host
  host="$(target_host)"

  if is_connected; then
    local pid
    pid=$(<"$PID_FILE")
    echo "VPN 已连接，PID: $pid"
  else
    echo 'VPN 未连接'
  fi

  echo "默认路由:"
  default_route_line || true

  if [[ -n "$host" ]]; then
    echo "目标路由:"
    route_line_for_host "$host" || true
  else
    echo '目标路由: 未配置 [ssh].target_host'
  fi
}

connect_vpn() {
  require_tool sudo
  require_tool "$(openconnect_bin)"
  if is_macos; then
    require_tool route
    require_tool nc
  else
    require_tool ip
  fi
  require_value OCH_VPN_HOST "未设置 [vpn].host，请在 ${OCH_CONFIG_FILE} 中配置"
  require_value OCH_VPN_USER "未设置 [vpn].user，请在 ${OCH_CONFIG_FILE} 中配置"

  if is_connected; then
    echo 'VPN 已连接，无需重复连接'
    show_status
    return 0
  fi

  local vpn_password vpn_script
  vpn_password=$(read_vpn_password)
  vpn_script=$(resolve_vpn_script)

  : >"$LOG_FILE"
  chmod 600 "$LOG_FILE"

  local -a openconnect_args=(
    "$OCH_VPN_HOST"
    -u "$OCH_VPN_USER"
    --os=win
    --useragent=AnyConnect
    --passwd-on-stdin
    --background
    --pid-file="$PID_FILE"
  )

  if [[ -n "$vpn_script" ]]; then
    openconnect_args+=(--script "$vpn_script")
  fi

  if [[ -n "${OCH_VPN_AUTHGROUP:-}" ]]; then
    openconnect_args+=(--authgroup="$OCH_VPN_AUTHGROUP")
  fi

  # shellcheck disable=SC2024
  printf '%s\n' "$vpn_password" | sudo_cmd env "OCH_ROUTES_EXTRA=${OCH_ROUTES_EXTRA:-}" "$(openconnect_bin)" "${openconnect_args[@]}" \
    >>"$LOG_FILE" 2>&1 || {
      unset vpn_password VPN_PASSWORD vpn_script
      echo "VPN 连接失败，日志见: $LOG_FILE" >&2
      tail -n 40 "$LOG_FILE" >&2 || true
      return 1
    }

  unset vpn_password VPN_PASSWORD vpn_script
  sleep 2

  if is_connected; then
    echo "VPN 已连接，日志: $LOG_FILE"
    if wait_for_target_route 15; then
      if [[ -n "$(target_host)" ]]; then
        verify_connection || true
      fi
    else
      echo '提示: VPN 进程已建立，但目标路由暂未就绪；可稍后手动执行 verify 再检查' >&2
    fi
    return 0
  fi

  echo "VPN 连接未建立，日志见: $LOG_FILE" >&2
  tail -n 40 "$LOG_FILE" >&2 || true
  return 1
}

disconnect_vpn() {
  require_tool sudo

  if ! [[ -s "$PID_FILE" ]]; then
    echo '未找到 PID 文件，视为已断开'
    return 0
  fi

  local pid
  pid=$(<"$PID_FILE")

  if sudo_cmd kill -0 "$pid" >/dev/null 2>&1; then
    sudo_cmd kill "$pid"
    sleep 1
    echo 'VPN 已断开'
  else
    echo '发现陈旧 PID 文件，已清理'
  fi

  sudo_cmd rm -f "$PID_FILE"
}

verify_connection() {
  if is_macos; then
    require_tool route
    require_tool nc
  else
    require_tool ip
  fi

  local host port default_iface="" target_iface=""
  host="$(target_host)"
  port="$(target_port)"
  [[ -n "$host" ]] || error "未设置 [ssh].target_host，无法验证目标连通性"

  default_iface=$(default_route_iface || true)
  target_iface=$(route_iface_for_host "$host" || true)

  echo "默认路由:"
  default_route_line
  echo "目标路由:"
  route_line_for_host "$host"

  if [[ -n "$default_iface" && -n "$target_iface" && "$default_iface" != "$target_iface" ]]; then
    echo "路由检查: 目标主机走 ${target_iface}，默认流量仍走 ${default_iface}"
  elif [[ -n "$target_iface" ]]; then
    echo "路由检查: 目标主机走 ${target_iface}，与默认路由相同；这可能是全隧道或服务端未下发分流路由"
  else
    echo '路由检查: 未能解析目标路由，请确认 VPN 已连接'
  fi

  if check_tcp_port "$host" "$port"; then
    echo "端口检查: ${host}:${port} 可达"
  else
    echo "端口检查: ${host}:${port} 不可达" >&2
    return 1
  fi
}

check_tcp_port() {
  local host="$1"
  local port="$2"

  if is_macos && nc -G 5 -z "$host" "$port" >/dev/null 2>&1; then
    return 0
  fi

  if command -v timeout >/dev/null 2>&1; then
    timeout 5 bash -lc "exec 3<>/dev/tcp/\$1/\$2" _ "$host" "$port" 2>/dev/null
    return $?
  fi

  if nc -G 5 -z "$host" "$port" >/dev/null 2>&1; then
    return 0
  fi

  nc -w 5 -z "$host" "$port" >/dev/null 2>&1
}

ssh_target() {
  require_tool ssh

  local host port user
  host="$(target_host)"
  port="$(target_port)"
  user="$(target_user)"
  [[ -n "$host" ]] || error "未设置 [ssh].target_host，无法发起 SSH 连接"

  if ! is_connected; then
    error 'VPN 未连接，请先执行 connect'
  fi

  exec ssh -p "$port" "${user}@${host}"
}

show_logs() {
  if [[ -f "$LOG_FILE" ]]; then
    tail -n 40 "$LOG_FILE"
  else
    echo "日志文件不存在: $LOG_FILE"
  fi
}

main() {
  local command="${1:-help}"

  case "$command" in
    connect)
      connect_vpn
      ;;
    disconnect)
      disconnect_vpn
      ;;
    status)
      show_status
      ;;
    verify)
      verify_connection
      ;;
    ssh)
      ssh_target
      ;;
    logs)
      show_logs
      ;;
    help|-h|--help)
      usage
      ;;
    *)
      error "未知命令: $command"
      ;;
  esac
}

# Only dispatch when executed directly, so tests can source this file and call
# individual functions (e.g. route parsing) without running a command.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
