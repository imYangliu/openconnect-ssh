#!/usr/bin/env bash
set -euo pipefail

: "${VPN_HOST:=}"
: "${VPN_USER:=}"
: "${VPN_AUTHGROUP:=}"
: "${TARGET_HOST:=}"
: "${TARGET_CIDR:=}"
: "${TARGET_PORT:=22}"
: "${TARGET_SSH_USER:=${USER:-root}}"
: "${OPENCONNECT_BIN:=$(command -v openconnect 2>/dev/null || printf 'openconnect')}"
: "${PID_FILE:=/tmp/ecnu-openconnect-${USER}.pid}"
: "${LOG_FILE:=/tmp/ecnu-openconnect-${USER}.log}"
: "${CONFIG_FILE:=$HOME/.config/ecnu-connect-campus-server.env}"

if [[ -r "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

: "${VPN_ROUTES:=${TARGET_CIDR:-}}"

usage() {
  cat <<EOF
AnyConnect / OpenConnect 单机分流脚本

用法:
  $(basename "$0") <command>

命令:
  connect      连接 VPN，并让指定网段走目标网络
  disconnect   断开 VPN
  status       显示当前连接状态
  verify       验证目标路由和 TARGET_HOST:TARGET_PORT 连通性
  ssh          通过 VPN SSH 到 TARGET_SSH_USER@TARGET_HOST:TARGET_PORT
  logs         查看最近日志
  help         显示帮助

环境变量:
  VPN_HOST         VPN 地址；必须配置
  VPN_USER         VPN 用户名；必须配置
  VPN_AUTHGROUP    可选认证组；部分网关需要
  TARGET_HOST      目标主机；verify / ssh 需要
  VPN_ROUTES       分流网段列表；connect 需要
  TARGET_CIDR      兼容旧变量；未设置 VPN_ROUTES 时也可用
  TARGET_PORT      目标端口，默认 ${TARGET_PORT}
  TARGET_SSH_USER  SSH 用户，默认 ${TARGET_SSH_USER}
  OPENCONNECT_BIN  openconnect 可执行文件，默认 ${OPENCONNECT_BIN}
  VPN_SCRIPT_CMD   可选；覆盖分流脚本命令，默认自动使用 vpn-slice 或 uvx
  VPN_PASSWORD     可选；未设置时会静默提示输入
  CONFIG_FILE      可选；默认 ${CONFIG_FILE}，可放 VPN_PASSWORD 等私密配置
  PID_FILE         PID 文件路径，默认 ${PID_FILE}
  LOG_FILE         日志文件路径，默认 ${LOG_FILE}

示例:
  $(basename "$0") connect
  $(basename "$0") verify
  $(basename "$0") ssh
  $(basename "$0") disconnect
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

resolve_vpn_script() {
  if [[ -n "${VPN_SCRIPT_CMD:-}" ]]; then
    printf '%s' "$VPN_SCRIPT_CMD"
    return 0
  fi

  if command -v vpn-slice >/dev/null 2>&1; then
    printf '%s %s' "$(command -v vpn-slice)" "$VPN_ROUTES"
    return 0
  fi

  if command -v uvx >/dev/null 2>&1; then
    printf '%s --from vpn-slice vpn-slice %s' "$(command -v uvx)" "$VPN_ROUTES"
    return 0
  fi

  error '缺少分流脚本：请安装 vpn-slice，或确保 uvx 可用'
}

read_vpn_password() {
  if [[ -n "${VPN_PASSWORD:-}" ]]; then
    printf '%s' "$VPN_PASSWORD"
    return 0
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

wait_for_target_route() {
  local timeout_seconds="${1:-15}"
  local default_iface=""
  local target_iface=""
  local attempt

  [[ -n "$TARGET_HOST" ]] || return 0

  default_iface=$(ip route show default | awk '/default/ {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')

  for ((attempt=0; attempt<timeout_seconds; attempt++)); do
    target_iface=$(ip route get "$TARGET_HOST" | awk '{for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')
    if [[ -n "$default_iface" && -n "$target_iface" && "$default_iface" != "$target_iface" ]]; then
      return 0
    fi
    sleep 1
  done

  return 1
}

show_status() {
  if is_connected; then
    local pid
    pid=$(<"$PID_FILE")
    echo "VPN 已连接，PID: $pid"
  else
    echo 'VPN 未连接'
  fi

  echo "默认路由:"
  ip route show default | sed -n '1p' || true

  if [[ -n "$TARGET_HOST" ]]; then
    echo "目标路由:"
    ip route get "$TARGET_HOST" | sed -n '1p' || true
  else
    echo '目标路由: 未配置 TARGET_HOST'
  fi
}

connect_vpn() {
  require_tool sudo
  require_tool "$OPENCONNECT_BIN"
  require_tool ip
  require_value VPN_HOST "未设置 VPN_HOST，请在 ${CONFIG_FILE} 或环境变量中配置"
  require_value VPN_USER "未设置 VPN_USER，请在 ${CONFIG_FILE} 或环境变量中配置"
  require_value VPN_ROUTES "未设置 VPN_ROUTES，请在 ${CONFIG_FILE} 或环境变量中配置"

  if is_connected; then
    echo 'VPN 已连接，无需重复连接'
    show_status
    return 0
  fi

  local vpn_password
  local vpn_script
  vpn_password=$(read_vpn_password)
  vpn_script=$(resolve_vpn_script)

  : >"$LOG_FILE"
  chmod 600 "$LOG_FILE"

  local -a openconnect_args=(
    "$VPN_HOST"
    -u "$VPN_USER"
    --os=win \
    --useragent=AnyConnect \
    --passwd-on-stdin \
    --background \
    --pid-file="$PID_FILE" \
    --script "$vpn_script" \
  )

  if [[ -n "$VPN_AUTHGROUP" ]]; then
    openconnect_args+=(--authgroup="$VPN_AUTHGROUP")
  fi

  # shellcheck disable=SC2024
  printf '%s\n' "$vpn_password" | sudo "$OPENCONNECT_BIN" "${openconnect_args[@]}" \
    >>"$LOG_FILE" 2>&1 || {
      unset vpn_password VPN_PASSWORD vpn_script VPN_SCRIPT_CMD
      echo "VPN 连接失败，日志见: $LOG_FILE" >&2
      tail -n 40 "$LOG_FILE" >&2 || true
      return 1
    }

  unset vpn_password VPN_PASSWORD vpn_script VPN_SCRIPT_CMD
  sleep 2

  if is_connected; then
    echo "VPN 已连接，日志: $LOG_FILE"
    if wait_for_target_route 15; then
      if [[ -n "$TARGET_HOST" ]]; then
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

  if sudo kill -0 "$pid" >/dev/null 2>&1; then
    sudo kill "$pid"
    sleep 1
    echo 'VPN 已断开'
  else
    echo '发现陈旧 PID 文件，已清理'
  fi

  sudo rm -f "$PID_FILE"
}

verify_connection() {
  require_tool ip
  require_tool timeout
  require_value TARGET_HOST "未设置 TARGET_HOST，无法验证目标连通性"

  local default_iface=""
  local target_iface=""
  default_iface=$(ip route show default | awk '/default/ {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')
  target_iface=$(ip route get "$TARGET_HOST" | awk '{for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')

  echo "默认路由:"
  ip route show default | sed -n '1p'
  echo "目标路由:"
  ip route get "$TARGET_HOST" | sed -n '1p'

  if [[ -n "$default_iface" && -n "$target_iface" && "$default_iface" != "$target_iface" ]]; then
    echo "路由检查: 目标主机走 ${target_iface}，默认流量仍走 ${default_iface}"
  else
    echo '路由检查: 未观察到目标主机与默认路由分离，请确认 VPN 已连接且 vpn-slice 生效'
  fi

  if timeout 5 bash -lc "exec 3<>/dev/tcp/${TARGET_HOST}/${TARGET_PORT}" 2>/dev/null; then
    echo "端口检查: ${TARGET_HOST}:${TARGET_PORT} 可达"
  else
    echo "端口检查: ${TARGET_HOST}:${TARGET_PORT} 不可达" >&2
    return 1
  fi
}

ssh_target() {
  require_tool ssh
  require_value TARGET_HOST "未设置 TARGET_HOST，无法发起 SSH 连接"

  if ! is_connected; then
    error 'VPN 未连接，请先执行 connect'
  fi

  exec ssh -p "$TARGET_PORT" "${TARGET_SSH_USER}@${TARGET_HOST}"
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

main "$@"
