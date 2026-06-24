#!/usr/bin/env bash
set -euo pipefail

prompt="${1:-Administrator password required for OCH}"

osascript <<OSA
display dialog "$prompt" default answer "" with hidden answer buttons {"OK"} default button "OK"
text returned of result
OSA
