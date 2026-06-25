#!/usr/bin/env bash

och_config_trim() {
  local value="$1"
  value="${value#"${value%%[!$' \t\r\n']*}"}"
  value="${value%"${value##*[!$' \t\r\n']}"}"
  printf '%s' "$value"
}

och_config_error() {
  echo "Error: $*" >&2
  return 1
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

och_config_parse_array() {
  local value body item result=""
  value="$(och_config_trim "$1")"
  [[ "$value" == \[*\] ]] || return 1
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
  local section="$1" key="$2" value="$3" line_number="$4"
  local full_key="$section.$key"

  if [[ "$full_key" != "routes.extra" ]]; then
    value="$(och_config_unquote "$value")"
  fi

  case "$full_key" in
    vpn.host)
      OCH_VPN_HOST="$value"
      ;;
    vpn.user)
      OCH_VPN_USER="$value"
      ;;
    vpn.auth_group)
      OCH_VPN_AUTHGROUP="$value"
      ;;
    ssh.host)
      OCH_SSH_HOST="$value"
      ;;
    ssh.target_host)
      OCH_TARGET_HOST="$value"
      ;;
    ssh.user)
      OCH_TARGET_SSH_USER="$value"
      ;;
    ssh.port)
      OCH_TARGET_PORT="$value"
      ;;
    routes.extra)
      OCH_ROUTES_EXTRA="$(och_config_parse_array "$value")" || \
        return $?
      ;;
    routes.mode)
      case "$value" in
        openconnect|extra)
          OCH_ROUTES_MODE="$value"
          ;;
        *)
          och_config_error "invalid routes.mode at line $line_number: $value" || return 1
          ;;
      esac
      ;;
    dns.mode)
      case "$value" in
        openconnect|ignore)
          OCH_DNS_MODE="$value"
          ;;
        *)
          och_config_error "invalid dns.mode at line $line_number: $value" || return 1
          ;;
      esac
      ;;
    proxy.local_host)
      OCH_PROXY_ENABLED=1
      OCH_PROXY_LOCAL_HOST="$value"
      ;;
    proxy.local_port)
      OCH_PROXY_ENABLED=1
      OCH_PROXY_LOCAL_PORT="$value"
      ;;
    proxy.remote_port)
      OCH_PROXY_ENABLED=1
      OCH_PROXY_REMOTE_PORT="$value"
      ;;
    app.language)
      case "$value" in
        system|en|zh-Hans|zh-Hant)
          OCH_APP_LANGUAGE="$value"
          ;;
        *)
          och_config_error "invalid app.language at line $line_number: $value" || return 1
          ;;
      esac
      ;;
    paths.*)
      och_config_error "[paths] is fixed by the installed runtime layout; remove $key at line $line_number" || return 1
      ;;
    .*|*.*)
      och_config_error "unknown config key at line $line_number: $full_key" || return 1
      ;;
  esac
}

och_config_init_defaults() {
  OCH_VPN_HOST="${OCH_VPN_HOST:-}"
  OCH_VPN_USER="${OCH_VPN_USER:-}"
  OCH_VPN_AUTHGROUP="${OCH_VPN_AUTHGROUP:-}"
  OCH_SSH_HOST="${OCH_SSH_HOST:-}"
  OCH_TARGET_HOST="${OCH_TARGET_HOST:-}"
  OCH_TARGET_PORT="${OCH_TARGET_PORT:-22}"
  OCH_TARGET_SSH_USER="${OCH_TARGET_SSH_USER:-${USER:-}}"
  OCH_ROUTES_MODE="${OCH_ROUTES_MODE:-openconnect}"
  OCH_ROUTES_EXTRA="${OCH_ROUTES_EXTRA:-}"
  OCH_DNS_MODE="${OCH_DNS_MODE:-openconnect}"
  OCH_PROXY_ENABLED="${OCH_PROXY_ENABLED:-0}"
  OCH_PROXY_LOCAL_HOST="${OCH_PROXY_LOCAL_HOST:-127.0.0.1}"
  OCH_PROXY_LOCAL_PORT="${OCH_PROXY_LOCAL_PORT:-7890}"
  OCH_PROXY_REMOTE_PORT="${OCH_PROXY_REMOTE_PORT:-7890}"
  OCH_APP_LANGUAGE="${OCH_APP_LANGUAGE:-system}"
}

och_config_validate_required() {
  local missing=0

  for name in \
    OCH_VPN_HOST \
    OCH_VPN_USER \
    OCH_SSH_HOST \
    OCH_TARGET_HOST; do
    if [[ -z "${!name:-}" ]]; then
      echo "Error: missing required config value: $name" >&2
      missing=1
    fi
  done

  [[ "$missing" -eq 0 ]]
}

load_och_toml_file() {
  local config_file="$1" validate_required="${2:-1}"
  [[ -r "$config_file" ]] || return 0

  OCH_VPN_HOST=""
  OCH_VPN_USER=""
  OCH_VPN_AUTHGROUP=""
  OCH_SSH_HOST=""
  OCH_TARGET_HOST=""
  OCH_TARGET_PORT="22"
  OCH_TARGET_SSH_USER="${USER:-}"
  OCH_ROUTES_MODE=""
  OCH_ROUTES_EXTRA=""
  OCH_DNS_MODE="openconnect"
  OCH_PROXY_LOCAL_HOST="127.0.0.1"
  OCH_PROXY_LOCAL_PORT="7890"
  OCH_PROXY_REMOTE_PORT="7890"
  OCH_APP_LANGUAGE="system"

  local section="" raw line key value line_number=0
  while IFS= read -r raw || [[ -n "$raw" ]]; do
    line_number=$((line_number + 1))
    line="$(och_config_trim "$(och_config_strip_comment "$raw")")"
    [[ -n "$line" ]] || continue

    if [[ "$line" == \[*\] ]]; then
      section="$(och_config_trim "${line:1:${#line}-2}")"
      case "$section" in
        vpn|ssh|routes|dns|proxy|paths|app)
          ;;
        *)
          och_config_error "unknown config section at line $line_number: $section" || return 1
          ;;
      esac
      continue
    fi

    [[ "$line" == *"="* ]] || {
      och_config_error "invalid config line $line_number: $line"
      return 1
    }
    [[ -n "$section" ]] || {
      och_config_error "config key outside a section at line $line_number: $line"
      return 1
    }

    key="$(och_config_trim "${line%%=*}")"
    value="$(och_config_trim "${line#*=}")"
    [[ -n "$key" ]] || {
      och_config_error "empty config key at line $line_number"
      return 1
    }
    och_config_apply_value "$section" "$key" "$value" "$line_number" || return 1
  done < "$config_file"

  if [[ -z "${OCH_ROUTES_MODE:-}" ]]; then
    if [[ -n "${OCH_ROUTES_EXTRA:-}" ]]; then
      OCH_ROUTES_MODE="extra"
    else
      OCH_ROUTES_MODE="openconnect"
    fi
  fi

  if [[ "$validate_required" == "1" ]]; then
    och_config_validate_required || return 1
  fi
}

och_config_init_defaults

och_config_file_mode() {
  local file="$1"
  if stat -f '%Lp' "$file" >/dev/null 2>&1; then
    stat -f '%Lp' "$file"
  else
    stat -c '%a' "$file"
  fi
}

load_och_secrets_file() {
  local secrets_file="$1"
  [[ -e "$secrets_file" ]] || return 0
  [[ -r "$secrets_file" ]] || {
    och_config_error "cannot read secrets file: $secrets_file"
    return 1
  }

  local mode
  mode="$(och_config_file_mode "$secrets_file")"
  [[ "$mode" == "600" ]] || {
    och_config_error "secrets file must have 0600 permissions: $secrets_file"
    return 1
  }

  local raw line key value line_number=0
  while IFS= read -r raw || [[ -n "$raw" ]]; do
    line_number=$((line_number + 1))
    line="$(och_config_trim "$(och_config_strip_comment "$raw")")"
    [[ -n "$line" ]] || continue
    [[ "$line" == *"="* ]] || {
      och_config_error "invalid secrets line $line_number: $line"
      return 1
    }

    key="$(och_config_trim "${line%%=*}")"
    value="$(och_config_trim "${line#*=}")"
    case "$key" in
      VPN_PASSWORD)
        if [[ -z "${VPN_PASSWORD:-}" ]]; then
          VPN_PASSWORD="$(och_config_unquote "$value")"
          export VPN_PASSWORD
        fi
        ;;
      *)
        och_config_error "unsupported secret key at line $line_number: $key"
        return 1
        ;;
    esac
  done < "$secrets_file"
}
