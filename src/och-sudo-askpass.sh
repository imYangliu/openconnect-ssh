#!/usr/bin/env bash
set -euo pipefail

prompt="${1:-Administrator password for OCH}"
case "$prompt" in
  Password:*|password:*|\[sudo\]*)
    prompt="Administrator password for OCH"
    ;;
esac

osascript <<OSA
display dialog "$prompt" default answer "" with hidden answer buttons {"OK"} default button "OK"
text returned of result
OSA
