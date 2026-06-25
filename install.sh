#!/usr/bin/env bash
set -euo pipefail

OCH_REPO="${OCH_REPO:-imyangliu/openconnect-ssh}"
OCH_VERSION="${OCH_VERSION:-latest}"
OCH_GITHUB_API_URL="${OCH_GITHUB_API_URL:-https://api.github.com/repos/${OCH_REPO}}"
OCH_RELEASE_BASE_URL="${OCH_RELEASE_BASE_URL:-https://github.com/${OCH_REPO}/releases/download}"

INSTALL_DEPS=1
DEPS_ONLY=0
MODE="install"

usage() {
  cat <<EOF
Usage:
  install.sh [options]

Options:
  --update          Run as an upgrade; same install path, different log wording
  --no-deps         Do not install system dependencies
  --deps-only       Install system dependencies and stop
  --prefix <path>   Install prefix; default is /opt/homebrew on macOS, /usr/local on Linux
  -h, --help        Show this help

Environment:
  PREFIX                 Same as --prefix
  BIN_DIR                Default: \$PREFIX/bin
  LIBEXEC_DIR            Default: \$PREFIX/libexec/och
  CONFIG_DIR             Default: /etc/och
  OCH_VERSION            Release tag to install; default: latest
  OCH_RELEASE_BASE_URL   Release download base URL
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --update)
      MODE="update"
      shift
      ;;
    --no-deps)
      INSTALL_DEPS=0
      shift
      ;;
    --deps-only)
      DEPS_ONLY=1
      shift
      ;;
    --prefix)
      [[ -n "${2:-}" ]] || {
        echo "Error: --prefix requires a path" >&2
        exit 2
      }
      PREFIX="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Error: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

log() {
  printf '[och-install] %s\n' "$*" >&2
}

die() {
  log "Error: $*"
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

download() {
  local url="$1"
  local output="$2"

  case "$url" in
    file://*)
      cp "${url#file://}" "$output"
      return 0
      ;;
  esac

  if have curl; then
    curl -fsSL "$url" -o "$output"
  elif have wget; then
    wget -qO "$output" "$url"
  else
    die "需要 curl 或 wget 下载 OCH release"
  fi
}

download_stdout() {
  local url="$1"

  case "$url" in
    file://*)
      cat "${url#file://}"
      return 0
      ;;
  esac

  if have curl; then
    curl -fsSL "$url"
  elif have wget; then
    wget -qO- "$url"
  else
    die "需要 curl 或 wget 查询 OCH latest release"
  fi
}

as_root() {
  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
  else
    have sudo || die "需要 sudo 来安装到系统目录，请先安装 sudo，或设置 PREFIX 到用户可写目录"
    sudo "$@"
  fi
}

nearest_existing_parent() {
  local path="$1"

  while [[ ! -e "$path" ]]; do
    path="$(dirname "$path")"
  done

  printf '%s\n' "$path"
}

path_needs_sudo() {
  local path="$1"
  local parent

  [[ "${EUID}" -ne 0 ]] || return 1
  parent="$(nearest_existing_parent "$path")"
  [[ ! -w "$parent" ]]
}

install_command() {
  local target_path="$1"
  shift

  if path_needs_sudo "$target_path"; then
    as_root "$@"
  else
    "$@"
  fi
}

detect_os() {
  OS_NAME="${OCH_OS_NAME:-$(uname -s)}"
  ARCH_NAME="${OCH_ARCH:-$(uname -m)}"
  OS_ID="${OCH_OS_ID:-}"
  OS_ID_LIKE="${OCH_OS_ID_LIKE:-}"

  if [[ "$OS_NAME" == "Linux" && -z "$OS_ID" && -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-}"
    OS_ID_LIKE="${ID_LIKE:-}"
  fi
}

platform_key() {
  case "${OS_NAME}/${ARCH_NAME}" in
    Darwin/arm64)
      printf '%s\n' darwin-arm64
      ;;
    Linux/x86_64|Linux/amd64)
      printf '%s\n' linux-x86_64
      ;;
    *)
      die "不支持的系统架构: ${OS_NAME}/${ARCH_NAME}。首版二进制仅支持 macOS arm64 和 Linux x86_64"
      ;;
  esac
}

default_prefix() {
  if [[ -n "${PREFIX:-}" ]]; then
    printf '%s\n' "$PREFIX"
    return 0
  fi

  if [[ "$OS_NAME" == "Darwin" ]]; then
    printf '%s\n' /opt/homebrew
    return 0
  fi

  printf '%s\n' /usr/local
}

is_debian_like() {
  [[ "$OS_ID" == "debian" || "$OS_ID" == "ubuntu" || " $OS_ID_LIKE " == *" debian "* ]]
}

install_macos_deps() {
  local -a packages=()
  have openconnect || packages+=(openconnect)

  if [[ "${#packages[@]}" -eq 0 ]]; then
    log "macOS 运行依赖已满足"
    return 0
  fi

  have brew || die "macOS 安装依赖需要 Homebrew。请先安装 Homebrew，或用 --no-deps 跳过依赖安装"

  log "安装 macOS 运行依赖: ${packages[*]}"
  brew install "${packages[@]}"
}

install_debian_deps() {
  have apt-get || die "当前 Linux 发行版缺少 apt-get；仅自动支持 Debian/Ubuntu 系发行版"

  log "安装 Debian/Ubuntu 运行依赖"
  as_root apt-get update
  as_root apt-get install -y \
    ca-certificates \
    curl \
    gzip \
    iproute2 \
    netcat-openbsd \
    openconnect \
    openssh-client \
    sudo \
    tar
}

install_system_deps() {
  if [[ "$INSTALL_DEPS" -eq 0 ]]; then
    log "跳过依赖安装"
    return 0
  fi

  case "$OS_NAME" in
    Darwin)
      install_macos_deps
      ;;
    Linux)
      if is_debian_like; then
        install_debian_deps
      else
        die "暂不支持自动安装此 Linux 发行版依赖: ${OS_ID:-unknown}。请手动安装 openconnect、openssh-client、nc、iproute2 后用 --no-deps 运行"
      fi
      ;;
    *)
      die "不支持的系统: $OS_NAME"
      ;;
  esac
}

resolve_version() {
  if [[ "$OCH_VERSION" != "latest" ]]; then
    printf '%s\n' "$OCH_VERSION"
    return 0
  fi

  local metadata tag
  metadata="$(download_stdout "${OCH_GITHUB_API_URL}/releases/latest")"
  tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$metadata" | head -n 1)"
  [[ -n "$tag" ]] || die "无法解析 latest release tag"
  printf '%s\n' "$tag"
}

artifact_name() {
  local version="$1"
  local platform="$2"
  printf 'och-cli-%s-%s.tar.gz\n' "$version" "$platform"
}

artifact_url() {
  local version="$1"
  local name="$2"

  case "$OCH_RELEASE_BASE_URL" in
    file://*)
      printf '%s/%s\n' "${OCH_RELEASE_BASE_URL%/}" "$name"
      ;;
    /*|.*)
      printf 'file://%s/%s\n' "${OCH_RELEASE_BASE_URL%/}" "$name"
      ;;
    *)
      printf '%s/%s/%s\n' "${OCH_RELEASE_BASE_URL%/}" "$version" "$name"
      ;;
  esac
}

package_root() {
  local extract_dir="$1"
  local first

  if [[ -x "$extract_dir/bin/och" ]]; then
    printf '%s\n' "$extract_dir"
    return 0
  fi

  first="$(find "$extract_dir" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
  if [[ -n "$first" && -x "$first/bin/och" ]]; then
    printf '%s\n' "$first"
    return 0
  fi

  die "release 包格式不正确：缺少 bin/och"
}

validate_package() {
  local root="$1"

  [[ -x "$root/bin/och" ]] || die "release 包缺少可执行文件: bin/och"
  [[ -f "$root/libexec/och/och-setup.sh" ]] || die "release 包缺少 helper: libexec/och/och-setup.sh"
  [[ -f "$root/libexec/och/macos-vpnc-route-wrapper.sh" ]] || die "release 包缺少 helper: libexec/och/macos-vpnc-route-wrapper.sh"
  [[ -f "$root/examples/ssh_config.example" ]] || die "release 包缺少示例配置: examples/ssh_config.example"
}

install_package() {
  local root="$1"
  local prefix="$2"
  local bin_dir="${BIN_DIR:-$prefix/bin}"
  local libexec_dir="${LIBEXEC_DIR:-$prefix/libexec/och}"
  local config_dir="${CONFIG_DIR:-/etc/och}"

  validate_package "$root"

  log "安装 och 到 $bin_dir"
  install_command "$bin_dir" install -d "$bin_dir"
  install_command "$libexec_dir" install -d "$libexec_dir"
  install_command "$config_dir" install -d "$config_dir"

  install_command "$bin_dir/och" install -m 0755 "$root/bin/och" "$bin_dir/och"

  local helper
  while IFS= read -r helper; do
    install_command "$libexec_dir/$(basename "$helper")" install -m 0755 "$helper" "$libexec_dir/$(basename "$helper")"
  done < <(find "$root/libexec/och" -maxdepth 1 -type f | sort)

  install_command "$config_dir/ssh_config.example" install -m 0644 "$root/examples/ssh_config.example" \
    "$config_dir/ssh_config.example"

  log "完成 ${MODE}:"
  log "  CLI: $bin_dir/och"
  log "  helpers: $libexec_dir"
  log "  examples: $config_dir"
}

fetch_and_install() {
  local platform="$1"
  local prefix="$2"
  local version name url archive extract_dir root

  version="$(resolve_version)"
  name="$(artifact_name "$version" "$platform")"
  url="$(artifact_url "$version" "$name")"

  TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/och-install.XXXXXX")"
  trap 'rm -rf "${TMP_DIR:-}"' EXIT

  archive="$TMP_DIR/$name"
  extract_dir="$TMP_DIR/package"

  log "下载 OCH release: $url"
  if ! download "$url" "$archive"; then
    die "没有找到匹配的 release 二进制: $name"
  fi

  mkdir -p "$extract_dir"
  tar -xzf "$archive" -C "$extract_dir"
  root="$(package_root "$extract_dir")"
  install_package "$root" "$prefix"
}

main() {
  detect_os
  local platform prefix
  platform="$(platform_key)"
  prefix="$(default_prefix)"
  PREFIX="$prefix"
  BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
  LIBEXEC_DIR="${LIBEXEC_DIR:-$PREFIX/libexec/och}"
  CONFIG_DIR="${CONFIG_DIR:-/etc/och}"

  log "系统: $OS_NAME/$ARCH_NAME ($platform)"
  log "安装前缀: $PREFIX"

  install_system_deps

  if [[ "$DEPS_ONLY" -eq 1 ]]; then
    log "依赖安装完成"
    return 0
  fi

  fetch_and_install "$platform" "$PREFIX"

  cat <<EOF

Next steps:
  1. Run: och setup
  2. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config
  3. Upgrade later with: och update
EOF
}

if [[ "${OCH_INSTALL_LIBRARY_MODE:-0}" != "1" ]]; then
  main "$@"
fi
