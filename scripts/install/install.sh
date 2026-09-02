#!/usr/bin/env sh
# mihari one-line installer for Linux and macOS.
#   curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.sh | bash -s -- --channel dev
#
# Environment overrides:
#   MIHARI_REPO        owner/repo (default mihari-proxy/mihari)
#   MIHARI_BIN         install dir (default /usr/local/bin)
#   MIHARI_VERSION     release tag to install (default: channel latest)
#   MIHARI_CHANNEL     main|dev when --channel is omitted
#   MIHARI_NO_INSTALL=1  download only; skip service install
set -eu

REPO="${MIHARI_REPO:-mihari-proxy/mihari}"
BIN_DIR="${MIHARI_BIN:-/usr/local/bin}"
GITHUB_API="${MIHARI_GITHUB_API:-https://api.github.com}"
CHANNEL=""
CHANNEL_EXPLICIT=0

info() { printf '\033[1;34m•\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --channel)
      [ $# -ge 2 ] || err "missing --channel value"
      [ -n "$2" ] || err "missing --channel value"
      CHANNEL="$2"
      CHANNEL_EXPLICIT=1
      shift 2
      ;;
    --channel=*)
      CHANNEL="${1#--channel=}"
      CHANNEL_EXPLICIT=1
      [ -n "$CHANNEL" ] || err "missing --channel value"
      shift
      ;;
    --)
      shift
      break
      ;;
    -*)
      err "unknown flag: $1"
      ;;
    *)
      err "unexpected argument: $1"
      ;;
  esac
done
[ $# -eq 0 ] || err "unexpected argument: $1"

if [ "$CHANNEL_EXPLICIT" -eq 0 ] && [ -n "${MIHARI_CHANNEL:-}" ]; then
  CHANNEL="$MIHARI_CHANNEL"
  CHANNEL_EXPLICIT=1
fi
if [ -n "$CHANNEL" ]; then
  case "$CHANNEL" in
    main|dev) ;;
    *) err "mihari channel must be main or dev" ;;
  esac
fi

# Detect OS.
if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  os="${MIHARI_TEST_OS:-linux}"
  arch="${MIHARI_TEST_ARCH:-amd64}"
else
  os="$(uname -s)"
  case "$os" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *) err "unsupported OS: $os" ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) err "unsupported architecture: $arch" ;;
  esac
fi

# Pick a downloader. dl writes to a file; fetch writes to stdout.
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
  fetch_headers_body() { curl -fsSL -D "$2" -o "$3" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO- "$1"; }
  fetch_headers_body() { wget -qS -O "$3" "$1" 2>"$2"; }
else
  err "need curl or wget"
fi

is_canonical_dev() {
  printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$'
}

# Draft filtering is best-effort: POSIX extraction matches canonical tags only.
extract_tag_names() {
  printf '%s' "$1" | tr '{' '\n' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

next_release_link() {
  tr -d '\r' < "$1" | awk 'tolower($1)=="link:" { $1=""; print substr($0,2) }' | tr ',' '\n' | while IFS= read -r part; do
    case "$part" in
      *rel=\"next\"*)
        printf '%s' "$part" | sed -n 's/.*<\([^>]*\)>.*/\1/p'
        break
        ;;
    esac
  done
}

tag_cmp() {
  left="$1"
  right="$2"
  lbody="${left#v}"
  rbody="${right#v}"
  lmaj="${lbody%%.*}"; lbody="${lbody#*.}"
  lmin="${lbody%%.*}"; lbody="${lbody#*.}"
  rmaj="${rbody%%.*}"; rbody="${rbody#*.}"
  rmin="${rbody%%.*}"; rbody="${rbody#*.}"
  lisdev=0
  risdev=0
  case "$lbody" in
    *-dev.*) lpat="${lbody%%-dev.*}"; ldev="${lbody#*-dev.}"; lisdev=1 ;;
    *) lpat="$lbody"; ldev=0 ;;
  esac
  case "$rbody" in
    *-dev.*) rpat="${rbody%%-dev.*}"; rdev="${rbody#*-dev.}"; risdev=1 ;;
    *) rpat="$rbody"; rdev=0 ;;
  esac
  if [ "$lmaj" -ne "$rmaj" ]; then [ "$lmaj" -gt "$rmaj" ] && echo 1 || echo -1; return; fi
  if [ "$lmin" -ne "$rmin" ]; then [ "$lmin" -gt "$rmin" ] && echo 1 || echo -1; return; fi
  if [ "$lpat" -ne "$rpat" ]; then [ "$lpat" -gt "$rpat" ] && echo 1 || echo -1; return; fi
  if [ "$lisdev" -ne "$risdev" ]; then
    [ "$lisdev" -eq 1 ] && echo -1 || echo 1
    return
  fi
  if [ "$lisdev" -eq 1 ]; then
    if [ "$ldev" -ne "$rdev" ]; then [ "$ldev" -gt "$rdev" ] && echo 1 || echo -1; return; fi
  fi
  echo 0
}

resolve_dev_tag() {
  url="${GITHUB_API}/repos/${REPO}/releases?per_page=100"
  best=""
  page=0
  tmpdir="$(mktemp -d)"
  while [ "$page" -lt 5 ]; do
    hdr="$tmpdir/hdr"
    body="$tmpdir/body"
    fetch_headers_body "$url" "$hdr" "$body" || {
      rm -rf "$tmpdir"
      err "failed to list GitHub Releases; set MIHARI_VERSION=vX.Y.Z-dev.N"
    }
    size="$(wc -c < "$body" | tr -d ' ')"
    if [ "$size" -gt 8388608 ]; then
      rm -rf "$tmpdir"
      err "mihari release list is too large; set MIHARI_VERSION=vX.Y.Z-dev.N"
    fi
    raw="$(cat "$body")"
    tags="$(extract_tag_names "$raw")"
    if [ -n "$tags" ]; then
      printf '%s\n' "$tags" | while IFS= read -r tag; do
        is_canonical_dev "$tag" || continue
        current=""
        [ -f "$tmpdir/best" ] && current="$(cat "$tmpdir/best")"
        if [ -z "$current" ] || [ "$(tag_cmp "$tag" "$current")" = "1" ]; then
          printf '%s\n' "$tag" >"$tmpdir/best"
        fi
      done
      if [ -f "$tmpdir/best" ]; then
        best="$(cat "$tmpdir/best")"
      fi
    fi
    next="$(next_release_link "$hdr")"
    [ -n "$next" ] || break
    url="$next"
    page=$((page + 1))
  done
  rm -rf "$tmpdir"
  [ -n "$best" ] || err "no canonical mihari dev release; set MIHARI_VERSION=vX.Y.Z-dev.N"
  printf '%s\n' "$best"
}

channel_data_root() {
  if [ -n "${MIHARI_DATA:-}" ]; then
    printf '%s\n' "$MIHARI_DATA"
    return
  fi
  if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
    case "$SUDO_USER" in
      *[!A-Za-z0-9._-]*|'') err "resolve sudo user home" ;;
    esac
    home=""
    if command -v getent >/dev/null 2>&1; then
      home="$(getent passwd "$SUDO_USER" | cut -d: -f6 || true)"
    fi
    if [ -z "$home" ]; then
      home="$(eval echo "~$SUDO_USER")"
    fi
    case "$home" in
      /*) ;;
      *) err "resolve mihari channel data root: home is not absolute" ;;
    esac
    [ -n "$home" ] || err "resolve sudo user home"
    printf '%s\n' "$home/.mihari"
    return
  fi
  printf '%s\n' "${HOME}/.mihari"
}

write_channel() {
  channel="$1"
  root="$(channel_data_root)"
  created=0
  [ -d "$root" ] || created=1
  mkdir -p "$root"
  tmp="$(mktemp "$root/.mihari-channel.tmp.XXXXXX")"
  printf '%s\n' "$channel" >"$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$root/mihari-channel"
  if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
    uid="$(id -u "$SUDO_USER")"
    gid="$(id -g "$SUDO_USER")"
    if [ "$created" -eq 1 ]; then
      chown "$uid:$gid" "$root"
    fi
    chown "$uid:$gid" "$root/mihari-channel"
  fi
}

looks_like_tag() {
  printf '%s' "$1" | grep -Eq '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$'
}

asset="mihari-${os}-${arch}"
target_tag="${MIHARI_VERSION:-}"
if [ -n "${MIHARI_VERSION:-}" ]; then
  url="https://github.com/${REPO}/releases/download/${MIHARI_VERSION}/${asset}"
elif [ "$CHANNEL" = "dev" ]; then
  target_tag="$(resolve_dev_tag)"
  url="https://github.com/${REPO}/releases/download/${target_tag}/${asset}"
else
  url="https://github.com/${REPO}/releases/latest/download/${asset}"
fi

installed=""
if [ -n "${MIHARI_TEST_INSTALLED_VERSION:-}" ]; then
  installed="${MIHARI_TEST_INSTALLED_VERSION}"
elif [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ] && [ -x "${BIN_DIR}/mihari" ]; then
  installed="$("${BIN_DIR}/mihari" self version 2>/dev/null | tr -d '\r' | head -n 1 || true)"
fi

downgrade=0
if looks_like_tag "$installed" && looks_like_tag "$target_tag"; then
  if [ "$(tag_cmp "$installed" "$target_tag")" = "1" ]; then
    downgrade=1
  fi
fi

if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
    write_channel "$CHANNEL"
  fi
  printf 'CHANNEL=%s\n' "$CHANNEL"
  printf 'EXPLICIT=%s\n' "$CHANNEL_EXPLICIT"
  printf 'URL=%s\n' "$url"
  printf 'TARGET_TAG=%s\n' "$target_tag"
  printf 'INSTALLED=%s\n' "$installed"
  printf 'DOWNGRADE=%s\n' "$downgrade"
  exit 0
fi

if [ "$downgrade" = "1" ]; then
  info "Installing older Mihari ${target_tag} over ${installed}."
  info "Settings, subscriptions, and generated files from the current version may be unsupported, fail to load, or look like they disappeared."
  info "Downgrade is not a supported config migration and does not roll disk state back."
  if [ "${MIHARI_YES:-0}" != "1" ]; then
    printf 'Continue with downgrade? [y/N] '
    reply=""
    if [ -t 0 ]; then
      read reply || err "cancelled"
    else
      read reply </dev/tty 2>/dev/null || err "downgrade requires confirmation; rerun with MIHARI_YES=1"
    fi
    case "$reply" in
      y|Y|yes|YES) ;;
      *) err "cancelled" ;;
    esac
  fi
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

if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
  write_channel "$CHANNEL"
fi

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
