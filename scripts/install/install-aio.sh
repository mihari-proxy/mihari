#!/usr/bin/env sh
# Local AIO install: pass the original .tar.gz release artifact. MIHARI_VERSION
# selects its fixed release tag; offline use requires an existing trusted root
# apply binary and root-preplaced install-trust resources. No bundle script runs.
set -eu

CHANNEL=""
CHANNEL_EXPLICIT=0
bundle_dir=""

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
    -*)
      err "unknown flag: $1"
      ;;
    *)
      [ -z "$bundle_dir" ] || err "unexpected extra argument: $1"
      bundle_dir="$1"
      shift
      ;;
  esac
done
if [ -z "$bundle_dir" ]; then
  bundle_dir="$(cd "$(dirname "$0")" && pwd)"
fi
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

if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  printf 'CHANNEL=%s\nEXPLICIT=%s\n' "$CHANNEL" "$CHANNEL_EXPLICIT"
  exit 0
fi
[ -f "$bundle_dir" ] || err "pass the original AIO .tar.gz archive"
archive=$(cd "$(dirname "$bundle_dir")" && printf '%s/%s' "$(pwd -P)" "$(basename "$bundle_dir")")
[ -n "${MIHARI_VERSION:-}" ] || err "set MIHARI_VERSION to the archive's fixed release tag"
if [ "${MIHARI_NO_INSTALL:-0}" = "1" ]; then info "Archive retained at $archive"; exit 0; fi
umask 077
candidate_dir=$(mktemp -d)
trap 'rm -f "$candidate_dir/mihari"; rmdir "$candidate_dir"' EXIT
trap 'exit 1' HUP INT TERM
# Extract only inert binary bytes; the privileged Go use case verifies the whole
# original archive and all typed resources before publication.
tar -xOzf "$archive" mihari | head -c 268435457 > "$candidate_dir/mihari"
[ "$(wc -c < "$candidate_dir/mihari")" -le 268435456 ] || err "binary exceeds limit"
root_apply "$MIHARI_VERSION" "${CHANNEL:-main}" "$candidate_dir/mihari" "$archive"
