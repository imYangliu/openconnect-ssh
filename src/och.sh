#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OCH_CONFIG_FILE="${OCH_CONFIG_FILE:-$HOME/.config/och/config.toml}"
OCH_SECRETS_FILE="${OCH_SECRETS_FILE:-$HOME/.config/och/secrets.env}"
OCH_COMMAND_NAME="${OCH_COMMAND_NAME:-$(basename "$0")}"
OCH_VPN_HELPER="$SCRIPT_DIR/och-vpn.sh"

# shellcheck disable=SC1091
source "$SCRIPT_DIR/och-config.sh"

if [[ -r "$OCH_CONFIG_FILE" ]]; then
  load_och_toml_file "$OCH_CONFIG_FILE"
fi
load_och_secrets_file "$OCH_SECRETS_FILE"

log() {
  echo "[och] $*" >&2
}

die() {
  log "$*"
  exit 1
}

usage() {
  cat <<EOF
用法:
  ${OCH_COMMAND_NAME} setup
  ${OCH_COMMAND_NAME} [ssh 参数...]
  ${OCH_COMMAND_NAME} --proxy [ssh 参数...]
  ${OCH_COMMAND_NAME} --proxy-command <host> <port>

说明:
  这是一个面向 AnyConnect / OpenConnect 场景的 SSH 包装器。
  在执行 ssh 之前会先验证 VPN 连通性；若断开，则自动尝试重连。

行为:
  - 若参数里已经包含目标主机，则按原样转交给 ssh
  - 若未提供目标主机，则使用 config.toml 的 [ssh].host；未配置则报错
  - 使用 --proxy 时，会额外添加 config.toml 中的反向端口映射
  - 使用 --proxy-command 时，会先确保 VPN 可达，再把 stdio 连接到目标 host:port
  - 使用 setup 时，会引导写入 ${OCH_CONFIG_FILE}、Keychain 和托管 SSH Host

示例:
  ${OCH_COMMAND_NAME} och-target
  ${OCH_COMMAND_NAME} --proxy och-target
  ${OCH_COMMAND_NAME} --proxy -N och-target
  ${OCH_COMMAND_NAME} -L 8080:127.0.0.1:8080 och-target
  ${OCH_COMMAND_NAME} --proxy-command %h %p

环境变量:
  OCH_CONFIG_FILE     OCH TOML 配置文件，默认 ${OCH_CONFIG_FILE}
  OCH_SECRETS_FILE    只含 VPN_PASSWORD 的 secret 文件，默认 ${OCH_SECRETS_FILE}
  VPN_PASSWORD        可选；优先于 secret 文件和 Keychain
EOF
}

require_tool() {
  local tool="$1"

  if [[ "$tool" == */* ]]; then
    [[ -x "$tool" ]] || die "缺少可执行文件: $tool"
    return 0
  fi

  command -v "$tool" >/dev/null 2>&1 || die "缺少依赖命令: $tool"
}

is_flag_cluster() {
  [[ "$1" =~ ^-[46AaCfGgKkMNnqsTtVvXxYy]+$ ]]
}

option_takes_value() {
  case "$1" in
    -B|-b|-c|-D|-E|-e|-F|-I|-i|-J|-L|-l|-m|-O|-o|-p|-Q|-R|-S|-W|-w)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

find_destination() {
  local arg
  local expect_value=0
  local stop_option_parsing=0

  for arg in "$@"; do
    if (( expect_value )); then
      expect_value=0
      continue
    fi

    if (( stop_option_parsing )); then
      printf '%s\n' "$arg"
      return 0
    fi

    case "$arg" in
      --)
        stop_option_parsing=1
        ;;
      -*)
        if is_flag_cluster "$arg"; then
          continue
        fi

        if option_takes_value "${arg:0:2}"; then
          if [[ "${#arg}" -eq 2 ]]; then
            expect_value=1
          fi
          continue
        fi

        continue
        ;;
      *)
        printf '%s\n' "$arg"
        return 0
        ;;
    esac
  done

  return 1
}

strip_wrapper_args() {
  local arg
  local expect_value=0
  local destination_found=0

  WRAPPER_PROXY_MODE=0
  WRAPPER_SSH_ARGS=()

  for arg in "$@"; do
    if (( expect_value )); then
      WRAPPER_SSH_ARGS+=("$arg")
      expect_value=0
      continue
    fi

    if (( destination_found )); then
      WRAPPER_SSH_ARGS+=("$arg")
      continue
    fi

    case "$arg" in
      --proxy)
        WRAPPER_PROXY_MODE=1
        continue
        ;;
      --)
        WRAPPER_SSH_ARGS+=("$arg")
        destination_found=1
        continue
        ;;
      -*)
        WRAPPER_SSH_ARGS+=("$arg")
        if is_flag_cluster "$arg"; then
          continue
        fi

        if option_takes_value "${arg:0:2}" && [[ "${#arg}" -eq 2 ]]; then
          expect_value=1
        fi
        continue
        ;;
      *)
        WRAPPER_SSH_ARGS+=("$arg")
        destination_found=1
        continue
        ;;
    esac
  done
}

resolve_target() {
  local destination="$1"
  local resolved

  RESOLVED_HOST="$destination"
  RESOLVED_PORT=""
  RESOLVED_USER=""

  if ! resolved=$(ssh -G "$destination" 2>/dev/null); then
    return 0
  fi

  RESOLVED_HOST=$(awk '$1 == "hostname" { print $2; exit }' <<<"$resolved")
  RESOLVED_PORT=$(awk '$1 == "port" { print $2; exit }' <<<"$resolved")
  RESOLVED_USER=$(awk '$1 == "user" { print $2; exit }' <<<"$resolved")
}

run_connect_script() {
  local subcommand="$1"
  shift

  local -a env_args=()

  [[ -n "${RESOLVED_HOST:-}" ]] && env_args+=("OCH_RUNTIME_TARGET_HOST=${RESOLVED_HOST}")
  [[ -n "${RESOLVED_PORT:-}" ]] && env_args+=("OCH_RUNTIME_TARGET_PORT=${RESOLVED_PORT}")
  [[ -n "${RESOLVED_USER:-}" ]] && env_args+=("OCH_RUNTIME_TARGET_USER=${RESOLVED_USER}")

  env "${env_args[@]}" "$OCH_VPN_HELPER" "$subcommand" "$@"
}

verify_vpn() {
  run_connect_script verify >/dev/null 2>&1
}

ensure_vpn() {
  if verify_vpn; then
    log "VPN 连通性正常"
    return 0
  fi

  log "VPN 不可达，正在尝试连接"
  run_connect_script connect

  if verify_vpn; then
    log "VPN 已恢复"
    return 0
  fi

  die "重连后仍无法访问目标，检查日志：${OCH_VPN_HELPER} logs"
}

proxy_command() {
  local host="${1:-}"
  local port="${2:-}"

  [[ -n "$host" ]] || die "--proxy-command 缺少 host"
  [[ -n "$port" ]] || die "--proxy-command 缺少 port"
  require_tool nc

  RESOLVED_HOST="$host"
  RESOLVED_PORT="$port"
  RESOLVED_USER=""

  ensure_vpn
  exec nc "$host" "$port"
}

main() {
  require_tool ssh
  require_tool "$OCH_VPN_HELPER"

  if [[ "${1:-}" == "setup" ]]; then
    local setup_script="${OCH_SETUP_SCRIPT:-$SCRIPT_DIR/och-setup.sh}"
    require_tool "$setup_script"
    exec "$setup_script"
  fi

  if [[ "${1:-}" == "--proxy-command" ]]; then
    shift
    proxy_command "$@"
  fi

  strip_wrapper_args "$@"
  local -a ssh_args=("${WRAPPER_SSH_ARGS[@]+"${WRAPPER_SSH_ARGS[@]}"}")
  local destination=""
  local proxy_mode="${WRAPPER_PROXY_MODE}"

  if [[ "${#ssh_args[@]}" -eq 1 ]]; then
    case "${ssh_args[0]}" in
      -h|--help|help)
        usage
        exit 0
        ;;
    esac
  fi

  if ! destination=$(find_destination "${ssh_args[@]+"${ssh_args[@]}"}"); then
    if [[ -z "${OCH_SSH_HOST:-}" ]]; then
      die "未提供目标主机，请传入 SSH host，或在 ${OCH_CONFIG_FILE} 中设置 ssh.host"
    fi
    destination="$OCH_SSH_HOST"
    ssh_args+=("$destination")
  fi

  if (( proxy_mode )); then
    log "启用代理映射: 远端 ${OCH_PROXY_REMOTE_PORT} -> 本地 ${OCH_PROXY_LOCAL_HOST}:${OCH_PROXY_LOCAL_PORT}"
    ssh_args+=(
      -o "ExitOnForwardFailure=yes"
      -R "${OCH_PROXY_REMOTE_PORT}:${OCH_PROXY_LOCAL_HOST}:${OCH_PROXY_LOCAL_PORT}"
    )
  fi

  resolve_target "$destination"
  ensure_vpn

  exec ssh "${ssh_args[@]}"
}

main "$@"
