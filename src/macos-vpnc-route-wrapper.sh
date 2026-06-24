#!/usr/bin/env bash
set -euo pipefail

PATH="/sbin:/usr/sbin:$PATH"

# Detect the Homebrew prefix: /opt/homebrew on Apple Silicon, /usr/local on Intel.
default_vpnc_script() {
  local prefix
  for prefix in /opt/homebrew /usr/local; do
    if [[ -x "$prefix/etc/vpnc/vpnc-script" ]]; then
      printf '%s/etc/vpnc/vpnc-script' "$prefix"
      return 0
    fi
  done
  # Fall back to the Apple Silicon path; run_base_script reports if it is missing.
  printf '/opt/homebrew/etc/vpnc/vpnc-script'
}

: "${VPNC_SCRIPT_BASE:=$(default_vpnc_script)}"
: "${MACOS_EXTRA_ROUTES:=}"
: "${OCH_ROUTE_DRY_RUN:=0}"

run_base_script() {
  if [[ ! -x "$VPNC_SCRIPT_BASE" ]]; then
    echo "Error: missing executable vpnc-script: $VPNC_SCRIPT_BASE" >&2
    return 1
  fi

  "$VPNC_SCRIPT_BASE" "$@"
}

run_route() {
  if [[ "$OCH_ROUTE_DRY_RUN" == "1" ]]; then
    printf 'route'
    printf ' %q' "$@"
    printf '\n'
    return 0
  fi

  route "$@"
}

apply_extra_routes() {
  local action="$1"
  local cidr

  [[ -n "$MACOS_EXTRA_ROUTES" ]] || return 0
  [[ -n "${TUNDEV:-}" ]] || {
    echo 'Error: TUNDEV is required to adjust macOS extra routes' >&2
    return 1
  }

  for cidr in $MACOS_EXTRA_ROUTES; do
    case "$action" in
      add)
        run_route -n add -net "$cidr" -interface "$TUNDEV"
        ;;
      delete)
        run_route -n delete -net "$cidr" -interface "$TUNDEV" || true
        ;;
      *)
        echo "Error: unknown route action: $action" >&2
        return 1
        ;;
    esac
  done
}

main() {
  case "${reason:-}" in
    connect|reconnect)
      run_base_script "$@"
      apply_extra_routes add
      ;;
    disconnect)
      apply_extra_routes delete
      run_base_script "$@"
      ;;
    *)
      run_base_script "$@"
      ;;
  esac
}

# Only run when executed directly, so tests can source this file and call
# default_vpnc_script without invoking the base vpnc-script.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
