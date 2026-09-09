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


# BEGIN ROOT APPLY
# Generated from root-apply.sh.in; edit the template and run generate_root_apply.py.
# The privileged shell is fixed code; caller values travel only as positional
# arguments. The first executed mihari is a checked root installation or a
# fixed official release verified inside this root-exclusive staging directory.
root_apply() {
  elevate=""
  if [ "$(id -u)" -ne 0 ]; then
    [ -x /usr/bin/sudo ] || err "installation requires root or /usr/bin/sudo"
    elevate=/usr/bin/sudo
  fi
  $elevate /usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin /bin/sh -s -- "$1" "$2" "$3" "$4" "${MIHARI_SOURCE:-}" "${MIHARI_DATA:-}" "${MIHARI_ENDPOINT:-}" "${MIHARI_CREDENTIAL:-}" "${MIHARI_INSTALL_ROOT:-}" "${MIHARI_BIN:-/usr/local/bin}/mihari" <<'MIHARI_ROOT_APPLY'
set -eu
PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH
unset ENV BASH_ENV CDPATH
[ "$(id -u)" -eq 0 ] || exit 1
umask 077
fail() { printf '%s\n' "$1" >&2; exit 1; }
tag=$1; channel=$2; candidate=$3; bundle=$4; source=$5; data=$6; endpoint=$7; credential=$8; install_root=$9; shift 9; path_binary=$1
stage=$(mktemp -d /var/tmp/mihari-install.XXXXXXXX)
cleanup() { rm -f "$stage/entry" "$stage/candidate" "$stage/checksums" "$stage/latest" "$stage/request.json"; rmdir "$stage"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
root_fetch() {
  [ -x /usr/bin/curl ] || fail "trusted bootstrap requires /usr/bin/curl"
  /usr/bin/curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 --max-filesize 268435456 "$1" -o "$2"
}
checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail "unsupported OS";; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) fail "unsupported architecture";; esac
if [ -z "$tag" ]; then
  [ "$channel" = main ] || fail "set MIHARI_VERSION to a fixed dev tag"
  root_fetch https://api.github.com/repos/mihari-proxy/mihari/releases/latest "$stage/latest"
  [ "$(wc -c < "$stage/latest")" -le 1048576 ] || fail "release metadata exceeds limit"
  tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$stage/latest")
fi
printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$' || fail "invalid fixed release tag"
case "$tag:$channel" in *-dev.*:dev) :;; *-dev.*:*) fail "release channel mismatch";; *:main) :;; *) fail "release channel mismatch";; esac
trusted_entry() {
  entry_path=$1
  [ -f "$entry_path" ] && [ ! -L "$entry_path" ] || return 1
  if [ "$os" = linux ]; then links=$(stat -c %h "$entry_path"); else links=$(stat -f %l "$entry_path"); fi
  [ "$links" = 1 ] || return 1
  while [ "$entry_path" != / ]; do
    [ ! -L "$entry_path" ] || return 1
    if [ "$os" = linux ]; then
      owner=$(stat -c %u "$entry_path") || return 1
      mode=$(stat -c %a "$entry_path") || return 1
    else
      owner=$(stat -f %u "$entry_path") || return 1
      mode=$(stat -f %Lp "$entry_path") || return 1
      [ -z "$(ls -lde "$entry_path" | sed -n '2p')" ] || return 1
    fi
    [ "$owner" = 0 ] && [ "$((0$mode & 022))" -eq 0 ] || return 1
    entry_path=$(dirname "$entry_path")
  done
}
entry=/usr/local/lib/mihari/mihari
if ! trusted_entry "$entry"; then entry=""; fi
if [ -n "$entry" ] && ! "$entry" service apply --help >/dev/null 2>&1; then entry=""; fi
# An offline install uses a previously trusted root apply binary plus the Go
# constructor's root-owned install-trust manifest and hash-named resources.
if [ -z "$entry" ] || [ -z "$candidate" ]; then
  asset="mihari-$os-$arch"
  release="https://github.com/mihari-proxy/mihari/releases/download/$tag"
  root_fetch "$release/SHA256SUMS.txt" "$stage/checksums"
  [ "$(wc -c < "$stage/checksums")" -le 1048576 ] || fail "checksum manifest exceeds limit"
  expected=$(awk -v asset="$asset" '$2==asset || $2=="*"asset {print $1}' "$stage/checksums")
  printf '%s\n' "$expected" | grep -Eq '^[0-9a-f]{64}$' || fail "missing unique official binary checksum"
  root_fetch "$release/$asset" "$stage/entry"
  [ "$(checksum "$stage/entry")" = "$expected" ] || fail "official binary checksum mismatch"
  chmod 0700 "$stage/entry"
  # Run the verified release's installer so an older installed version cannot
  # prevent its own upgrade before the candidate is published.
  entry="$stage/entry"
  [ -n "$candidate" ] || candidate="$stage/entry"
fi
# BEGIN REQUEST JSON
json_string() {
  case "$1" in *'
'*) fail "newline in request value";; esac
  printf '%s' "$1" | LC_ALL=C grep '[[:cntrl:]]' >/dev/null && fail "control character in request value"
  printf '"%s"' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')"
}
json_field() { [ -n "$2" ] || return 0; printf ',"%s":' "$1"; json_string "$2"; }
write_request() {
  printf '{"schema":"mihari.install-request/v1","operation":"install","binary":'
  json_string "$candidate"
  json_field channel "$channel"
  if [ -n "$data" ]; then json_field layout private; json_field data "$data"; else json_field layout system; fi
  json_field source "$source"
  json_field endpoint "$endpoint"
  json_field credential "$credential"
  json_field install_root "$install_root"
  json_field path_binary "$path_binary"
  json_field release_tag "$tag"
  if [ -n "$bundle" ]; then json_field bundle "$bundle"; json_field bundle_sha256 "$(checksum "$bundle")"; fi
  printf '}\n'
}
# END REQUEST JSON
write_request > "$stage/request.json"
"$entry" service apply --request "$stage/request.json" --json
MIHARI_ROOT_APPLY
}
# END ROOT APPLY

asset="mihari-${os}-${arch}"
if [ -n "${MIHARI_VERSION:-}" ]; then
  url="https://github.com/${REPO}/releases/download/${MIHARI_VERSION}/${asset}"
elif [ "$CHANNEL" = "dev" ]; then
  tag="$(resolve_dev_tag)"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
else
  url="https://github.com/${REPO}/releases/latest/download/${asset}"
fi

if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  printf 'CHANNEL=%s\n' "$CHANNEL"
  printf 'EXPLICIT=%s\n' "$CHANNEL_EXPLICIT"
  printf 'URL=%s\n' "$url"
  exit 0
fi


if [ "${MIHARI_NO_INSTALL:-0}" = "1" ]; then
  output_dir="${MIHARI_DOWNLOAD_DIR:-.}"
  mkdir -p "$output_dir"
  umask 077
  dl "$url" "$output_dir/$asset" || err "download failed"
  info "Downloaded $output_dir/$asset"
  exit 0
fi
# Repo/API overrides select only unprivileged download artifacts. Root bootstrap
# independently resolves and verifies the official fixed-tag binary.
root_apply "${MIHARI_VERSION:-${tag:-}}" "${CHANNEL:-main}" "" ""
