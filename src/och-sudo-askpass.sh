#!/usr/bin/env bash
set -euo pipefail

prompt="${1:-Administrator password for OCH}"
case "$prompt" in
  Password:*|password:*|\[sudo\]*)
    prompt="Administrator password for OCH"
    ;;
esac

export OCH_ASKPASS_PROMPT="$prompt"
osascript <<OSA
set dialogPrompt to system attribute "OCH_ASKPASS_PROMPT"
display dialog dialogPrompt default answer "" with hidden answer buttons {"OK"} default button "OK"
text returned of result
OSA
