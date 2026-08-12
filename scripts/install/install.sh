#!/usr/bin/env sh
# mihari one-line installer for Linux and macOS.
#   curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
#
# Environment overrides:
#   MIHARI_REPO        owner/repo (default mihari-proxy/mihari)
#   MIHARI_BIN         install dir (default /usr/local/bin)
#   MIHARI_VERSION     release tag to install (default: latest)
#   MIHARI_NO_INSTALL=1  download only; skip service install
set -eu

REPO="${MIHARI_REPO:-mihari-proxy/mihari}"
BIN_DIR="${MIHARI_BIN:-/usr/local/bin}"

info() { printf '\033[1;34m•\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# Detect OS.
os="$(uname -s)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) err "unsupported OS: $os" ;;
esac

# Detect architecture.
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac

# Pick a downloader. dl writes to a file; fetch writes to stdout.
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO- "$1"; }
else
  err "need curl or wget"
fi

# Asset names carry no version (mihari-<os>-<arch>[.exe]), so the stable
# /releases/latest/download/ path works for the default case.
asset="mihari-${os}-${arch}"
if [ -n "${MIHARI_VERSION:-}" ]; then
  url="https://github.com/${REPO}/releases/download/${MIHARI_VERSION}/${asset}"
else
  url="https://github.com/${REPO}/releases/latest/download/${asset}"
fi

# Elevate for writes to system dirs when needed.
SUDO=""
if [ ! -w "$BIN_DIR" ] 2>/dev/null; then
  if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  fi
fi

tmp="$(mktemp)"
info "Downloading ${asset} from ${REPO}…"
dl "$url" "$tmp" || err "download failed: $url"

info "Installing to ${BIN_DIR}/mihari"
$SUDO mkdir -p "$BIN_DIR"
$SUDO install -m 0755 "$tmp" "${BIN_DIR}/mihari"
rm -f "$tmp"

if [ "${MIHARI_NO_INSTALL:-0}" = "1" ]; then
  info "Downloaded. Run: mihari daemon (or: mihari service install && mihari service start)"
  exit 0
fi

# OS service registration needs root/system-level permissions on most setups.
if [ "$(id -u)" -ne 0 ]; then
  info "Registering the OS service requires root; elevating…"
  if ! command -v sudo >/dev/null 2>&1; then
    err "service install needs root; rerun as root or set MIHARI_NO_INSTALL=1"
  fi
  $SUDO "${BIN_DIR}/mihari" service install
  $SUDO "${BIN_DIR}/mihari" service start
else
  "${BIN_DIR}/mihari" service install
  "${BIN_DIR}/mihari" service start
fi

printf '\n\033[1;32m✓ Done.\033[0m Manage with: mihari status | mihari sub add <url>\n'
