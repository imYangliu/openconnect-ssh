#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo 'Usage: connect-split.sh VPN_URL VPN_USER CIDR [CIDR ...]' >&2
  echo 'Requires openconnect, vpn-slice and permission to configure the system network.' >&2
  exit 2
fi
vpn_url="$1"
vpn_user="$2"
shift 2
slice="$(command -v vpn-slice)" || { echo 'vpn-slice is required' >&2; exit 1; }
command -v openconnect >/dev/null || { echo 'openconnect is required' >&2; exit 1; }
# OpenConnect passes this string to /bin/sh. Restrict every interpolated token.
[[ "$slice" =~ ^[a-zA-Z0-9_./+-]+$ ]] || { echo 'Unsupported vpn-slice path' >&2; exit 2; }
script="$slice"
for cidr in "$@"; do
  [[ "$cidr" =~ ^[0-9a-fA-F:.]+/[0-9]+$ ]] || { echo 'Expected a CIDR, not a shell expression' >&2; exit 2; }
  script+=" $cidr"
done
exec openconnect --protocol=anyconnect --user="$vpn_user" --script "$script" -- "$vpn_url"
