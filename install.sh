#!/usr/bin/env bash
set -euo pipefail

OCH_REPO_URL="${OCH_REPO_URL:-https://github.com/imyangliu/openconnect-ssh.git}"
OCH_TARBALL_URL="${OCH_TARBALL_URL:-https://github.com/imyangliu/openconnect-ssh/archive/refs/heads/main.tar.gz}"

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
  --prefix <path>   Install prefix; default is /opt/homebrew on Apple Silicon macOS, otherwise /usr/local
  -h, --help        Show this help

Environment:
  PREFIX            Same as --prefix
  BIN_DIR           Default: \$PREFIX/bin
  LIBEXEC_DIR       Default: \$PREFIX/libexec/och
  CONFIG_DIR        Default: /etc/och
  OCH_TARBALL_URL   Source archive used when the script is run outside a checkout
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

as_root() {
  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
  else
    have sudo || die "需要 sudo 来安装到系统目录，请先安装 sudo，或设置 PREFIX 到用户可写目录"
    sudo "$@"
  fi
}

install_command() {
  local first="$1"
  shift

  if [[ "${EUID}" -eq 0 || "$first" == "$HOME/"* || "$first" == "$HOME" ]]; then
    "$@"
  else
    as_root "$@"
  fi
}

detect_os() {
  OS_NAME="$(uname -s)"
  OS_ID=""
  OS_ID_LIKE=""

  if [[ "$OS_NAME" == "Linux" && -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-}"
    OS_ID_LIKE="${ID_LIKE:-}"
  fi
}

default_prefix() {
  if [[ -n "${PREFIX:-}" ]]; then
    printf '%s\n' "$PREFIX"
    return 0
  fi

  if [[ "$OS_NAME" == "Darwin" ]]; then
    if [[ "$(uname -m)" == "arm64" || -d /opt/homebrew ]]; then
      printf '%s\n' /opt/homebrew
    else
      printf '%s\n' /usr/local
    fi
    return 0
  fi

  printf '%s\n' /usr/local
}

is_debian_like() {
  [[ "$OS_ID" == "debian" || "$OS_ID" == "ubuntu" || " $OS_ID_LIKE " == *" debian "* ]]
}

install_macos_deps() {
  have brew || die "macOS 安装依赖需要 Homebrew。请先安装 Homebrew，或用 --no-deps 跳过依赖安装"

  local -a packages=()
  have openconnect || packages+=(openconnect)
  have cargo || packages+=(rust)

  if [[ "${#packages[@]}" -eq 0 ]]; then
    log "macOS 依赖已满足"
    return 0
  fi

  log "安装 macOS 依赖: ${packages[*]}"
  brew install "${packages[@]}"
}

install_debian_deps() {
  have apt-get || die "当前 Linux 发行版缺少 apt-get；仅自动支持 Debian/Ubuntu 系发行版"

  log "安装 Debian/Ubuntu 依赖"
  as_root apt-get update
  as_root apt-get install -y \
    bash \
    build-essential \
    ca-certificates \
    cargo \
    curl \
    gzip \
    iproute2 \
    netcat-openbsd \
    openconnect \
    openssh-client \
    rustc \
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
        die "暂不支持自动安装此 Linux 发行版依赖: ${OS_ID:-unknown}。可手动安装 openconnect、openssh-client、rust/cargo、nc、iproute2 后用 --no-deps 运行"
      fi
      ;;
    *)
      die "不支持的系统: $OS_NAME"
      ;;
  esac
}

script_dir() {
  local source_path="${BASH_SOURCE[0]:-$0}"
  if [[ -n "$source_path" && -f "$source_path" ]]; then
    cd "$(dirname "$source_path")" && pwd
  else
    pwd
  fi
}

is_source_tree() {
  local candidate="$1"
  [[ -f "$candidate/rust-cli/Cargo.toml" && -f "$candidate/src/och-setup.sh" ]]
}

download() {
  local url="$1"
  local output="$2"

  if have curl; then
    curl -fsSL "$url" -o "$output"
  elif have wget; then
    wget -qO "$output" "$url"
  else
    die "需要 curl 或 wget 下载 OCH 源码: $OCH_REPO_URL"
  fi
}

prepare_source_tree() {
  local initial_dir="$1"

  if is_source_tree "$initial_dir"; then
    ROOT_DIR="$initial_dir"
    return 0
  fi

  TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/och-install.XXXXXX")"
  trap 'rm -rf "${TMP_DIR:-}"' EXIT

  local archive="$TMP_DIR/och.tar.gz"
  local source_dir="$TMP_DIR/source"

  log "下载 OCH 源码: $OCH_TARBALL_URL"
  download "$OCH_TARBALL_URL" "$archive"
  mkdir -p "$source_dir"
  tar -xzf "$archive" -C "$source_dir" --strip-components 1

  is_source_tree "$source_dir" || die "下载的源码归档不完整"
  ROOT_DIR="$source_dir"
}

ensure_build_tools() {
  have cargo || die "缺少 cargo；请重新运行安装脚本并允许安装依赖，或先手动安装 Rust"
  have install || die "缺少 install 命令"
}

install_och() {
  local prefix="$1"
  local bin_dir="${BIN_DIR:-$prefix/bin}"
  local libexec_dir="${LIBEXEC_DIR:-$prefix/libexec/och}"
  local config_dir="${CONFIG_DIR:-/etc/och}"

  ensure_build_tools

  log "构建 Rust CLI"
  cargo build --manifest-path "$ROOT_DIR/rust-cli/Cargo.toml" --release

  log "安装 och 到 $bin_dir"
  install_command "$bin_dir" install -d "$bin_dir"
  install_command "$libexec_dir" install -d "$libexec_dir"
  install_command "$bin_dir" install -m 0755 "$ROOT_DIR/rust-cli/target/release/och" "$bin_dir/och"
  install_command "$libexec_dir" install -m 0755 "$ROOT_DIR/src/och-config.sh" "$libexec_dir/och-config.sh"
  install_command "$libexec_dir" install -m 0755 "$ROOT_DIR/src/och-setup.sh" "$libexec_dir/och-setup.sh"
  install_command "$libexec_dir" install -m 0755 "$ROOT_DIR/src/macos-vpnc-route-wrapper.sh" "$libexec_dir/macos-vpnc-route-wrapper.sh"
  install_command "$libexec_dir" install -m 0755 "$ROOT_DIR/src/och-sudo-askpass.sh" "$libexec_dir/och-sudo-askpass.sh"

  install_command "$config_dir" install -d "$config_dir"
  install_command "$config_dir" install -m 0644 "$ROOT_DIR/examples/ssh_config.example" \
    "$config_dir/ssh_config.example"

  log "完成 ${MODE}:"
  log "  CLI: $bin_dir/och"
  log "  helpers: $libexec_dir"
  log "  examples: $config_dir"
}

main() {
  detect_os
  PREFIX="$(default_prefix)"
  BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
  LIBEXEC_DIR="${LIBEXEC_DIR:-$PREFIX/libexec/och}"
  CONFIG_DIR="${CONFIG_DIR:-/etc/och}"

  log "系统: $OS_NAME ${OS_ID:-}"
  log "安装前缀: $PREFIX"

  install_system_deps

  if [[ "$DEPS_ONLY" -eq 1 ]]; then
    log "依赖安装完成"
    return 0
  fi

  prepare_source_tree "$(script_dir)"
  install_och "$PREFIX"

  cat <<EOF

Next steps:
  1. Run: och setup
  2. Optionally merge $CONFIG_DIR/ssh_config.example into ~/.ssh/config
  3. Upgrade later with: och update
EOF
}

main "$@"
