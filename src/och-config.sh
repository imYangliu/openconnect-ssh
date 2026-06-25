#!/usr/bin/env bash

och_config_trim() {
  local value="$1"
  value="${value#"${value%%[!$' \t\r\n']*}"}"
  value="${value%"${value##*[!$' \t\r\n']}"}"
  printf '%s' "$value"
}

och_config_strip_comment() {
  local line="$1"
  local result="" char
  local in_double=0 in_single=0 escaping=0

  local i
  for (( i = 0; i < ${#line}; i++ )); do
    char="${line:i:1}"
    if (( escaping )); then
      result+="$char"
      escaping=0
      continue
    fi

    if [[ "$char" == "\\" ]]; then
      result+="$char"
      (( in_double )) && escaping=1
      continue
    fi

    if [[ "$char" == '"' && "$in_single" -eq 0 ]]; then
      (( in_double = 1 - in_double ))
    elif [[ "$char" == "'" && "$in_double" -eq 0 ]]; then
      (( in_single = 1 - in_single ))
    elif [[ "$char" == "#" && "$in_double" -eq 0 && "$in_single" -eq 0 ]]; then
      break
    fi
    result+="$char"
  done

  printf '%s' "$result"
}

och_config_unquote() {
  local value
  value="$(och_config_trim "$1")"

  if [[ "${#value}" -ge 2 && "${value:0:1}" == '"' && "${value: -1}" == '"' ]]; then
    value="${value:1:${#value}-2}"
    value="${value//\\\"/\"}"
    value="${value//\\\\/\\}"
  elif [[ "${#value}" -ge 2 && "${value:0:1}" == "'" && "${value: -1}" == "'" ]]; then
    value="${value:1:${#value}-2}"
  fi

  printf '%s' "$value"
}

och_config_set_if_unset() {
  local name="$1" value="$2"
  [[ -z "${!name:-}" ]] || return 0
  printf -v "$name" '%s' "$value"
  export "${name?}"
}

och_config_parse_array() {
  local value body item result=""
  value="$(och_config_trim "$1")"
  [[ "$value" == \[*\] ]] || return 0
  body="${value:1:${#value}-2}"
  [[ -n "$(och_config_trim "$body")" ]] || return 0

  while IFS= read -r item || [[ -n "$item" ]]; do
    item="$(och_config_unquote "$item")"
    [[ -n "$item" ]] || continue
    if [[ -n "$result" ]]; then
      result+=" "
    fi
    result+="$item"
  done < <(printf '%s' "$body" | tr ',' '\n')

  printf '%s' "$result"
}

och_config_apply_value() {
  local section="$1" key="$2" value="$3"

  if [[ "$section.$key" != "routes.extra" ]]; then
    value="$(och_config_unquote "$value")"
  fi

  case "$section.$key" in
    vpn.host)
      och_config_set_if_unset VPN_HOST "$value"
      ;;
    vpn.user)
      och_config_set_if_unset VPN_USER "$value"
      ;;
    vpn.auth_group)
      och_config_set_if_unset VPN_AUTHGROUP "$value"
      ;;
    ssh.host)
      och_config_set_if_unset DEFAULT_HOST "$value"
      ;;
    ssh.target_host)
      och_config_set_if_unset TARGET_HOST "$value"
      ;;
    ssh.user)
      och_config_set_if_unset TARGET_SSH_USER "$value"
      ;;
    ssh.port)
      och_config_set_if_unset TARGET_PORT "$value"
      ;;
    routes.extra)
      value="$(och_config_parse_array "$value")"
      och_config_set_if_unset MACOS_EXTRA_ROUTES "$value"
      och_config_set_if_unset VPN_ROUTES "$value"
      ;;
    proxy.local_host)
      och_config_set_if_unset PROXY_LOCAL_HOST "$value"
      ;;
    proxy.local_port)
      och_config_set_if_unset PROXY_LOCAL_PORT "$value"
      ;;
    proxy.remote_port)
      och_config_set_if_unset PROXY_REMOTE_PORT "$value"
      ;;
    paths.och)
      och_config_set_if_unset OCH_PATH "$value"
      ;;
    paths.och_vpn)
      och_config_set_if_unset OCH_VPN_PATH "$value"
      och_config_set_if_unset CONNECT_SCRIPT "$value"
      ;;
    paths.askpass)
      och_config_set_if_unset OCH_ASKPASS_PATH "$value"
      och_config_set_if_unset SUDO_ASKPASS "$value"
      ;;
  esac
}

load_och_toml_file() {
  local config_file="$1"
  [[ -r "$config_file" ]] || return 0

  local section="" raw line key value
  while IFS= read -r raw || [[ -n "$raw" ]]; do
    line="$(och_config_trim "$(och_config_strip_comment "$raw")")"
    [[ -n "$line" ]] || continue

    if [[ "$line" == \[*\] ]]; then
      section="$(och_config_trim "${line:1:${#line}-2}")"
      continue
    fi

    [[ "$line" == *"="* ]] || continue
    key="$(och_config_trim "${line%%=*}")"
    value="$(och_config_trim "${line#*=}")"
    och_config_apply_value "$section" "$key" "$value"
  done < "$config_file"
}
