#!/usr/bin/env sh
# Mihari all-in-one remote downloader. Fetches an inert platform bundle from
# the AList transport index, then delegates a typed request to trusted root apply.
# Root apply independently verifies the official or root-offline release authority.
#   curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
#   sh install-aio-remote.sh [--yes|-y]
#
# A retained original tar.gz can be passed to install-aio.sh for offline reuse
# with an existing trusted root entry and administrator-preplaced trust resources.
#
# Environment overrides:
#   MIHARI_INDEX_URL   index.txt public direct link (default: the fixed public URL below)
#   MIHARI_BUNDLE_URL  explicit transport URL; root apply still verifies release authority
#   MIHARI_CHANNEL     main|dev when --channel is omitted
set -eu

STABLE_INDEX_URL="https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
DEV_INDEX_URL="https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"
BUNDLE_URL="${MIHARI_BUNDLE_URL:-}"
YES=0
CHANNEL=""
CHANNEL_EXPLICIT=0
while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y)
      YES=1
      shift
      ;;
    --channel)
      [ $# -ge 2 ] || { printf '\033[1;31merror:\033[0m missing --channel value\n' >&2; exit 1; }
      CHANNEL="$2"
      CHANNEL_EXPLICIT=1
      shift 2
      ;;
    --channel=*)
      CHANNEL="${1#--channel=}"
      CHANNEL_EXPLICIT=1
      [ -n "$CHANNEL" ] || { printf '\033[1;31merror:\033[0m missing --channel value\n' >&2; exit 1; }
      shift
      ;;
    -*)
      printf '\033[1;31merror:\033[0m unknown flag: %s\n' "$1" >&2
      exit 1
      ;;
    *)
      printf '\033[1;31merror:\033[0m unexpected argument: %s\n' "$1" >&2
      exit 1
      ;;
  esac
done
if [ "$CHANNEL_EXPLICIT" -eq 0 ] && [ -n "${MIHARI_CHANNEL:-}" ]; then
  CHANNEL="$MIHARI_CHANNEL"
  CHANNEL_EXPLICIT=1
fi
if [ -n "$CHANNEL" ]; then
  case "$CHANNEL" in
    main|dev) ;;
    *) printf '\033[1;31merror:\033[0m mihari channel must be main or dev\n' >&2; exit 1 ;;
  esac
fi
if [ -n "${MIHARI_INDEX_URL:-}" ]; then
  INDEX_URL="$MIHARI_INDEX_URL"
elif [ "$CHANNEL" = "dev" ]; then
  INDEX_URL="$DEV_INDEX_URL"
else
  INDEX_URL="$STABLE_INDEX_URL"
fi

info() { printf '\033[1;34m•\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# confirm: returns 0 (yes) / 1 (no). --yes bypasses. stdin tty → read; else
# /dev/tty (when piped from curl, real stdin is occupied but the user's tty is
# still readable); failure → default no (design §4.4 step 2).
confirm() {
  [ "$YES" = "1" ] && return 0
  printf '%s [y/N] ' "$1"
  reply=''
  if [ -t 0 ]; then
    read reply || return 1
  else
    read reply </dev/tty 2>/dev/null || return 1
  fi
  case "$reply" in
    y|Y|yes|YES|Yes) return 0 ;;
    *) return 1 ;;
  esac
}

# Detect platform (mirrors install.sh). Test mode loads only the downloader and
# therefore also works under Git Bash on the Windows CI runner.
if [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ]; then
  os="$(uname -s)"
  case "$os" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) err "unsupported OS: $os" ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) err "unsupported architecture: $arch" ;;
  esac
  platform="${os}-${arch}"
fi

# Downloader: dl writes to a file, fetch writes to stdout (mirrors install.sh).
if command -v curl >/dev/null 2>&1; then
  downloader="curl"
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  downloader="wget"
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO- "$1"; }
else
  err "need curl or wget"
fi

download_file_with_progress() {
  url="$1"
  dest="$2"
  if [ "$downloader" != "curl" ]; then
    dl "$url" "$dest"
    return
  fi

  probe_headers="${dest}.probe-headers-$$"
  probe_body="${dest}.probe-body-$$"
  probe_status="${dest}.probe-status-$$"
  parts_dir=""
  pids=""
  cancel_download() {
    trap - HUP INT TERM
    for cancel_pid in $pids; do kill "$cancel_pid" 2>/dev/null || true; done
    for cancel_pid in $pids; do wait "$cancel_pid" 2>/dev/null || true; done
    [ -z "$parts_dir" ] || rm -rf "$parts_dir"
    rm -f "$probe_headers" "$probe_body" "$probe_status" "$dest"
    exit 1
  }
  trap 'cancel_download' HUP INT TERM
  curl -fsSL -H 'Range: bytes=0-0' -D "$probe_headers" -o "$probe_body" -w '%{http_code}' "$url" >"$probe_status" 2>/dev/null &
  pids="$!"
  wait "$pids" 2>/dev/null || true
  pids=""
  status="$(cat "$probe_status" 2>/dev/null || true)"
  content_range="$(tr -d '\r' <"$probe_headers" | awk 'tolower($1) == "content-range:" {print $2 " " $3}' | tail -n 1)"
  rm -f "$probe_headers" "$probe_body" "$probe_status"
  total="$(printf '%s\n' "$content_range" | sed -n 's#^bytes 0-0/\([0-9][0-9]*\)$#\1#p')"
  if [ "$status" != "206" ] || [ -z "$total" ] || [ "$total" -lt 4 ]; then
    trap - HUP INT TERM
    dl "$url" "$dest"
    return
  fi

  parts_dir="$(mktemp -d "${dest}.parts-XXXXXX")"
  failed=0
  base_size=$((total / 4))
  index=0
  while [ "$index" -lt 4 ]; do
    start=$((index * base_size))
    if [ "$index" -eq 3 ]; then end=$((total - 1)); else end=$((start + base_size - 1)); fi
    part="${parts_dir}/part-$(printf '%02d' "$index")"
    header="${part}.headers"
    curl -fsSL --range "${start}-${end}" -D "$header" -o "$part" -w '%{http_code}' "$url" >"${part}.status" &
    pids="$pids $!"
    index=$((index + 1))
  done

  while :; do
    running=0
    for pid in $pids; do kill -0 "$pid" 2>/dev/null && running=1; done
    [ "$running" = 1 ] || break
    downloaded=0
    for progress_part in "$parts_dir"/part-[0-9][0-9]; do
      [ -f "$progress_part" ] || continue
      progress_size="$(wc -c <"$progress_part" | tr -d ' ')"
      downloaded=$((downloaded + progress_size))
    done
    percent=$((downloaded * 100 / total))
    printf '\r  downloaded %.1f / %.1f MB (%d%%)' "$(awk "BEGIN {print $downloaded/1048576}")" "$(awk "BEGIN {print $total/1048576}")" "$percent"
    sleep 0.1
  done
  for pid in $pids; do wait "$pid" || failed=1; done
  printf '\n'

  if [ "$failed" = 0 ]; then
    index=0
    while [ "$index" -lt 4 ]; do
      part="${parts_dir}/part-$(printf '%02d' "$index")"
      start=$((index * base_size))
      if [ "$index" -eq 3 ]; then end=$((total - 1)); else end=$((start + base_size - 1)); fi
      got_range="$(tr -d '\r' <"${part}.headers" | awk 'tolower($1) == "content-range:" {print $2 " " $3}' | tail -n 1)"
      [ "$(cat "${part}.status")" = "206" ] || failed=1
      [ "$got_range" = "bytes ${start}-${end}/${total}" ] || failed=1
      [ "$(wc -c <"$part" | tr -d ' ')" = "$((end - start + 1))" ] || failed=1
      index=$((index + 1))
    done
  fi

  if [ "$failed" = 0 ]; then
    : >"$dest"
    index=0
    while [ "$index" -lt 4 ]; do
      part="${parts_dir}/part-$(printf '%02d' "$index")"
      cat "$part" >>"$dest" || failed=1
      index=$((index + 1))
    done
    [ "$(wc -c <"$dest" | tr -d ' ')" = "$total" ] || failed=1
  fi
  rm -rf "$parts_dir"
  trap - HUP INT TERM
  if [ "$failed" != 0 ]; then
    rm -f "$dest"
    return 1
  fi
}

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else err "need sha256sum or shasum"
  fi
}

print_remote_test_state() {
  printf 'CHANNEL=%s\n' "$CHANNEL"
  printf 'EXPLICIT=%s\n' "$CHANNEL_EXPLICIT"
  printf 'INDEX_URL=%s\n' "$INDEX_URL"
  printf 'HANDOFF=%s\n' 'service apply --request <root-private-json>'
  printf 'LATEST=%s\n' "${latest:-}"
}

is_canonical_stable() {
  printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
}

is_canonical_dev() {
  printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$'
}

# Tests source this standalone script to exercise the real downloader against
# a local HTTP server without running the installation flow.
if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  case "$0" in
    *install-aio-remote.sh)
      if [ -z "$BUNDLE_URL" ] && [ -n "${MIHARI_INDEX_URL:-}" ]; then
        :
      else
        latest=""
        print_remote_test_state
        exit 0
      fi
      ;;
    *)
      return 0
      ;;
  esac
fi

# Resolve bundle URL + expected sha256 + latest version.
latest=""
bundle_url=""
want_sum=""
if [ -n "$BUNDLE_URL" ]; then
  bundle_url="$BUNDLE_URL"
else
  # index.txt line format: "<key> <rest...>". key="latest" → <version>;
  # key="<goos>-<goarch>" → <public_url> <sha256>.
  index="$(fetch "$INDEX_URL" 2>/dev/null || true)"
  [ -n "$index" ] || err "The release index is unavailable. Try again later or check network and storage availability."
  # Heredoc (not a pipe) so parsed values survive outside the loop's subshell.
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    case "$line" in '#'*|'//'*) continue ;; esac
    key="${line%% *}"
    rest="${line#* }"
    if [ "$key" = "latest" ]; then
      latest="${rest%% *}"
    elif [ "$key" = "$platform" ]; then
      bundle_url="${rest%% *}"
      want_sum="${rest#* }"
    fi
  done <<EOF
$index
EOF
  [ -n "$latest" ] || err "The index has no latest release. Publication may be in progress or the release may have been withdrawn."
  if [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ]; then
    [ -n "$bundle_url" ] || err "The index has no package for $platform."
  fi
  if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
    if [ "$CHANNEL" = "dev" ]; then
      is_canonical_dev "$latest" || err "dev index latest must be vX.Y.Z-dev.N"
    else
      is_canonical_stable "$latest" || err "main index latest must be vX.Y.Z"
    fi
  fi
fi

if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  print_remote_test_state
  exit 0
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

# Downloads remain user artifacts. The archive is never extracted over D and
# install-aio.sh inside the archive is never executed.
workdir="${MIHARI_DOWNLOAD_DIR:-.}"
mkdir -p "$workdir"
workdir=$(cd "$workdir" && pwd -P)
archive="$workdir/mihari-all-in-one-${platform}.tar.gz"
info "Downloading $bundle_url"
download_file_with_progress "$bundle_url" "$archive" || err "bundle download failed"
if [ -n "$want_sum" ]; then [ "$(checksum "$archive")" = "$want_sum" ] || err "transport checksum mismatch"; fi
if [ "${MIHARI_NO_INSTALL:-0}" = "1" ]; then info "Downloaded $archive"; exit 0; fi
release_tag="${MIHARI_VERSION:-$latest}"
[ -n "$release_tag" ] || err "set MIHARI_VERSION to the archive's fixed release tag"
umask 077
candidate_dir=$(mktemp -d)
trap 'rm -f "$candidate_dir/mihari"; rmdir "$candidate_dir"' EXIT
trap 'exit 1' HUP INT TERM
tar -xOzf "$archive" mihari | head -c 268435457 > "$candidate_dir/mihari"
[ "$(wc -c < "$candidate_dir/mihari")" -le 268435456 ] || err "binary exceeds limit"
root_apply "$release_tag" "${CHANNEL:-main}" "$candidate_dir/mihari" "$archive"
