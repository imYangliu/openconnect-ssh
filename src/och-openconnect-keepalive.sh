#!/usr/bin/env bash
set -euo pipefail

VPN_SERVICE="${VPN_SERVICE:-och-openconnect.service}"
CONFIG_FILE="${CONFIG_FILE:-$HOME/.config/och/och-vpn.env}"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-2}"
STATE_DIR="${STATE_DIR:-/run/och-openconnect-keepalive}"
FAIL_COUNT_FILE="${FAIL_COUNT_FILE:-${STATE_DIR}/fail-count}"
PROBE_HOST="${PROBE_HOST:-}"
PING_BIN="${PING_BIN:-ping}"
PING_COUNT="${PING_COUNT:-1}"
PING_TIMEOUT="${PING_TIMEOUT:-1}"

load_env_file() {
  local env_file="$1"

  [[ -r "$env_file" ]] || return 0
  set -a
  # shellcheck disable=SC1090
  source "$env_file"
  set +a
}

if [[ -r "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

if [[ -n "${ENV_FILE:-}" ]]; then
  load_env_file "$ENV_FILE"
elif [[ -r .env ]]; then
  load_env_file .env
elif [[ -n "${PROJECT_ENV_FILE:-}" ]]; then
  load_env_file "$PROJECT_ENV_FILE"
fi

PROBE_HOST="${PROBE_HOST:-${TARGET_HOST:-}}"

log() {
  echo "[och-openconnect-keepalive] $*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log "missing required command: $1"
    exit 1
  }
}

validate_positive_int() {
  local name="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[0-9]+$ ]] || (( value < 1 )); then
    log "${name} must be an integer >= 1, got: ${value}"
    exit 1
  fi
}

reset_fail_count() {
  rm -f "$FAIL_COUNT_FILE"
}

read_fail_count() {
  local count=0

  if [[ -r "$FAIL_COUNT_FILE" ]]; then
    read -r count <"$FAIL_COUNT_FILE" || true
  fi

  if ! [[ "$count" =~ ^[0-9]+$ ]]; then
    count=0
  fi

  printf '%s\n' "$count"
}

write_fail_count() {
  mkdir -p "$STATE_DIR"
  printf '%s\n' "$1" >"$FAIL_COUNT_FILE"
}

run_probe() {
  require_cmd "$PING_BIN"

  if [[ -z "$PROBE_HOST" ]]; then
    log "PROBE_HOST or TARGET_HOST is required for ping-based keepalive"
    return 1
  fi

  "$PING_BIN" -n -q -c "$PING_COUNT" -W "$PING_TIMEOUT" "$PROBE_HOST" >/dev/null 2>&1
}

main() {
  validate_positive_int "FAIL_THRESHOLD" "$FAIL_THRESHOLD"
  validate_positive_int "PING_COUNT" "$PING_COUNT"
  validate_positive_int "PING_TIMEOUT" "$PING_TIMEOUT"

  if ! systemctl is-active --quiet "$VPN_SERVICE"; then
    reset_fail_count
    log "${VPN_SERVICE} is inactive, skipping keepalive check"
    return 0
  fi

  if run_probe; then
    if [[ -e "$FAIL_COUNT_FILE" ]]; then
      log "ping to ${PROBE_HOST} recovered, clearing failure counter"
    fi
    reset_fail_count
    return 0
  fi

  local fail_count
  fail_count=$(read_fail_count)
  fail_count=$((fail_count + 1))
  write_fail_count "$fail_count"

  log "ping to ${PROBE_HOST} failed (${fail_count}/${FAIL_THRESHOLD})"
  if (( fail_count < FAIL_THRESHOLD )); then
    return 0
  fi

  log "failure threshold reached, restarting ${VPN_SERVICE}"
  reset_fail_count
  systemctl restart "$VPN_SERVICE"
}

main "$@"
