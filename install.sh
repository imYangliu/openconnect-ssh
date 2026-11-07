#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  echo 'Usage: install.sh [--prefix PATH]'
  echo 'Build and install the native WIP CLI from this checkout. Requires Go and make.'
  exit 0
fi
prefix="${PREFIX:-$HOME/.local}"
if [[ ${1:-} == --prefix && $# == 2 ]]; then
  prefix="$2"
elif [[ $# != 0 ]]; then
  echo 'Usage: install.sh [--prefix PATH]' >&2
  exit 2
fi
exec make install PREFIX="$prefix"
