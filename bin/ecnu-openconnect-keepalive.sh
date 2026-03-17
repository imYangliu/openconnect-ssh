#!/usr/bin/env bash
set -euo pipefail

VPN_SERVICE="${VPN_SERVICE:-ecnu-openconnect.service}"
CONNECT_SCRIPT="${CONNECT_SCRIPT:-/usr/local/bin/connect-campus-server.sh}"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-2}"
STATE_DIR="${STATE_DIR:-/run/ecnu-openconnect-keepalive}"
FAIL_COUNT_FILE="${FAIL_COUNT_FILE:-${STATE_DIR}/fail-count}"

log() {
  echo "[ecnu-openconnect-keepalive] $*"
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

main() {
  if ! [[ "$FAIL_THRESHOLD" =~ ^[0-9]+$ ]] || (( FAIL_THRESHOLD < 1 )); then
    log "FAIL_THRESHOLD must be an integer >= 1, got: ${FAIL_THRESHOLD}"
    exit 1
  fi

  if ! systemctl is-active --quiet "$VPN_SERVICE"; then
    reset_fail_count
    log "${VPN_SERVICE} is inactive, skipping keepalive check"
    return 0
  fi

  if "$CONNECT_SCRIPT" verify >/dev/null 2>&1; then
    if [[ -e "$FAIL_COUNT_FILE" ]]; then
      log "verify recovered, clearing failure counter"
    fi
    reset_fail_count
    return 0
  fi

  local fail_count
  fail_count=$(read_fail_count)
  fail_count=$((fail_count + 1))
  write_fail_count "$fail_count"

  log "verify failed (${fail_count}/${FAIL_THRESHOLD})"
  if (( fail_count < FAIL_THRESHOLD )); then
    return 0
  fi

  log "failure threshold reached, restarting ${VPN_SERVICE}"
  reset_fail_count
  systemctl restart "$VPN_SERVICE"
}

main "$@"
