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
confirm_macos_install() {
  printf '\033[1;33mwarning:\033[0m %s\n' "macOS is currently unsupported. Support is incomplete and use is not recommended." >&2
  if [ "${MIHARI_YES:-}" = "1" ] || [ "${YES:-0}" = "1" ]; then
    return 0
  fi
  printf 'Continue anyway? [y/N] ' >&2
  reply=''
  # Test mode must not read /dev/tty; a real read would block the test runner.
  if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
    reply="${MIHARI_TEST_MACOS_CONFIRM:-}"
  elif [ -t 0 ]; then
    IFS= read -r reply || reply=''
  else
    IFS= read -r reply </dev/tty 2>/dev/null || reply=''
  fi
  case "$reply" in
    y|Y|yes|YES|Yes) return 0 ;;
    *) return 1 ;;
  esac
}

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
if [ "$os" = "darwin" ]; then
  confirm_macos_install || err "Cancelled; macOS installation was not confirmed. Use MIHARI_YES=1 to continue anyway."
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
  explicit_yes=0
  [ "${MIHARI_YES:-}" != 1 ] || explicit_yes=1
  [ "${5:-0}" != 1 ] || explicit_yes=1
  elevate=""
  if [ "$(id -u)" -ne 0 ]; then
    [ -x /usr/bin/sudo ] || err "installation requires root or /usr/bin/sudo"
    elevate=/usr/bin/sudo
  fi
  $elevate /usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin /bin/sh -s -- "$1" "$2" "$3" "$4" "${MIHARI_SOURCE:-}" "${MIHARI_DATA:-}" "${MIHARI_ENDPOINT:-}" "${MIHARI_CREDENTIAL:-}" "${MIHARI_INSTALL_ROOT:-}" "${MIHARI_BIN:-/usr/local/bin}/mihari" "$explicit_yes" "${6:-offline}" <<'MIHARI_ROOT_APPLY'
set -eu
PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH
unset ENV BASH_ENV CDPATH
[ "$(id -u)" -eq 0 ] || exit 1
umask 077
fail() { printf '%s\n' "$1" >&2; exit 1; }
tag=$1; channel=$2; candidate=$3; bundle=$4; source=$5; data=$6; endpoint=$7; credential=$8; install_root=$9; shift 9; path_binary=$1; explicit_yes=$2; bootstrap_mode=$3
case "$bootstrap_mode" in online|offline) :;; *) fail "invalid bootstrap mode";; esac
stage=$(mktemp -d /var/tmp/mihari-install.XXXXXXXX)
cleanup() { rm -f "$stage/entry" "$stage/candidate" "$stage/checksums" "$stage/latest" "$stage/request.json" "$stage/result.json" "$stage/error.json" "$stage/helper-help" "$stage/helper-latest" "$stage/bundle" "$stage/mihari" "$stage/index" "$stage/index-headers" "$stage/index-status" "$stage/tar-status" "$stage/fetch-headers"; rmdir "$stage"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
root_fetch() {
  [ -x /usr/bin/curl ] || fail "trusted bootstrap requires /usr/bin/curl"
  if [ "${3:-}" = progress ]; then
    root_fetch_with_progress "$1" "$2"
    return
  fi
  /usr/bin/curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 --max-filesize 268435456 "$1" -o "$2"
}
root_fetch_with_progress() {
  fetch_url=$1
  fetch_dest=$2
  rm -f "$stage/fetch-headers"
  /usr/bin/curl --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 30 --head -D "$stage/fetch-headers" -o /dev/null "$fetch_url" || true
  fetch_total=$(awk 'BEGIN { n = "" } tolower($1) == "content-length:" { n = $2 } END { gsub(/\r/, "", n); print n }' "$stage/fetch-headers" 2>/dev/null || true)
  case "$fetch_total" in
    ""|*[!0-9]*) fetch_total=0 ;;
  esac
  if [ "$fetch_total" -le 0 ]; then
    /usr/bin/curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 --max-filesize 268435456 "$fetch_url" -o "$fetch_dest"
    return
  fi
  /usr/bin/curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 --max-filesize 268435456 "$fetch_url" -o "$fetch_dest" &
  fetch_pid=$!
  while kill -0 "$fetch_pid" 2>/dev/null; do
    fetch_got=0
    if [ -f "$fetch_dest" ]; then fetch_got=$(wc -c < "$fetch_dest" | tr -d ' '); fi
    fetch_percent=$((fetch_got * 100 / fetch_total))
    if [ "$fetch_percent" -gt 100 ]; then fetch_percent=100; fi
    printf '\r  downloaded %s / %s MB (%d%%)' "$(awk -v n="$fetch_got" 'BEGIN { printf "%.1f", n/1048576 }')" "$(awk -v n="$fetch_total" 'BEGIN { printf "%.1f", n/1048576 }')" "$fetch_percent"
    sleep 0.1
  done
  fetch_status=0
  wait "$fetch_pid" || fetch_status=$?
  fetch_got=0
  if [ -f "$fetch_dest" ]; then fetch_got=$(wc -c < "$fetch_dest" | tr -d ' '); fi
  fetch_percent=$((fetch_got * 100 / fetch_total))
  if [ "$fetch_percent" -gt 100 ]; then fetch_percent=100; fi
  printf '\r  downloaded %s / %s MB (%d%%)\n' "$(awk -v n="$fetch_got" 'BEGIN { printf "%.1f", n/1048576 }')" "$(awk -v n="$fetch_total" 'BEGIN { printf "%.1f", n/1048576 }')" "$fetch_percent"
  return "$fetch_status"
}
# Channel index is the trust root: refuse redirects and keep at most 65537 bytes.
# root_fetch still follows redirects for GitHub release assets.
root_fetch_index() {
  [ -x /usr/bin/curl ] || fail "trusted bootstrap requires /usr/bin/curl"
  rm -f "$2" "$stage/index-headers" "$stage/index-status"
  # head -c counts bytes across reads. dd count=1 can stop after one short
  # pipe read, and the pipeline status would hide curl's exit code.
  (
    set +e
    /usr/bin/curl --silent --show-error --proto '=https' --proto-redir '=https' --max-time 120 --dump-header "$stage/index-headers" --max-filesize 65536 -o - "$1"
    printf '%s\n' "$?" > "$stage/index-status"
  ) | head -c 65537 > "$2" || fail "cannot fetch channel index"
  index_status=$(tr -d '[:space:]' < "$stage/index-status") || fail "cannot fetch channel index"
  [ -f "$2" ] || fail "cannot fetch channel index"
  [ "$(wc -c < "$2")" -le 65536 ] || fail "channel index exceeds limit"
  [ "$index_status" = 0 ] || fail "cannot fetch channel index"
  index_code=$(awk 'BEGIN { code = "" } /^HTTP\// { code = $2 } END { gsub(/\r/, "", code); if (code == "") exit 1; print code }' "$stage/index-headers") || fail "cannot fetch channel index"
  [ "$index_code" = 200 ] || fail "cannot fetch channel index"
}
checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail "unsupported OS";; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) fail "unsupported architecture";; esac
if [ -z "$tag" ]; then
  [ -z "$candidate" ] || fail "Offline installation requires a fixed release tag; set MIHARI_VERSION."
  [ "$channel" = main ] || fail "set MIHARI_VERSION to a fixed dev tag"
  root_fetch https://api.github.com/repos/mihari-proxy/mihari/releases/latest "$stage/latest"
  [ "$(wc -c < "$stage/latest")" -le 1048576 ] || fail "release metadata exceeds limit"
  tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$stage/latest")
fi
printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$' || fail "invalid fixed release tag"
case "$tag:$channel" in *-dev.*:dev) :;; *-dev.*:*) fail "release channel mismatch";; *:main) :;; *) fail "release channel mismatch";; esac
trusted_entry() {
  root_path_trusted "$1"
}
entry=/usr/local/lib/mihari/mihari
helper_capable() {
  "$1" service apply --help >"$stage/helper-help" 2>/dev/null || return 1
  [ "$(wc -c < "$stage/helper-help")" -le 65536 ] || return 1
  grep -Eq '(^|[[:space:]])--yes([=[:space:]]|$)' "$stage/helper-help" &&
    grep -Eq '(^|[[:space:]])--expected-preview([=[:space:]]|$)' "$stage/helper-help"
}
verified_binary() {
  release="https://github.com/mihari-proxy/mihari/releases/download/$1"
  asset="mihari-$os-$arch"
  download_label=$3
  verify_label=$4
  printf '\033[1;34m•\033[0m %s\n' "$download_label"
  step_started=$(date +%s)
  root_fetch "$release/SHA256SUMS.txt" "$stage/checksums"
  [ "$(wc -c < "$stage/checksums")" -le 1048576 ] || fail "checksum manifest exceeds limit"
  expected=$(awk -v asset="$asset" '$2==asset || $2=="*"asset {print $1}' "$stage/checksums")
  printf '%s\n' "$expected" | grep -Eq '^[0-9a-f]{64}$' || fail "missing unique official binary checksum"
  root_fetch "$release/$asset" "$2" progress
  step_now=$(date +%s)
  printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
  printf '\033[1;34m•\033[0m %s\n' "$verify_label"
  step_started=$(date +%s)
  [ "$(checksum "$2")" = "$expected" ] || fail "official binary checksum mismatch"
  step_now=$(date +%s)
  printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
  chmod 0700 "$2"
}
root_path_trusted() {
  entry_path=$1
  case "$entry_path" in
    /*) ;;
    *) return 1 ;;
  esac
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
# manifest_pins_digest FILE ARCHIVE_SHA256 MIHARI_SHA256
# Accept only a 64-lowercase-hex digest in top-level bundles or binaries.
# A manifest larger than 1048576 bytes does not match.
manifest_pins_digest() {
  pin_file=$1
  pin_archive=$2
  pin_binary=$3
  [ -f "$pin_file" ] && [ ! -L "$pin_file" ] || return 1
  [ "$(wc -c < "$pin_file")" -le 1048576 ] || return 1
  printf '%s\n' "$pin_archive" | grep -Eq '^[0-9a-f]{64}$' || pin_archive=
  printf '%s\n' "$pin_binary" | grep -Eq '^[0-9a-f]{64}$' || pin_binary=
  [ -n "$pin_archive$pin_binary" ] || return 1
  LC_ALL=C awk -v archive="$pin_archive" -v binary="$pin_binary" '
    function failparse() { failed = 1; exit 1 }
    function skip_ws(   c) {
      while (p <= len) {
        c = substr(text, p, 1)
        if (c != " " && c != "\t" && c != "\n" && c != "\r") break
        p++
      }
    }
    function parse_string(   out, c) {
      if (substr(text, p, 1) != "\"") failparse()
      p++
      out = ""
      while (p <= len) {
        c = substr(text, p, 1)
        p++
        if (c == "\"") return out
        if (c == "\\") {
          if (p > len) failparse()
          c = substr(text, p, 1)
          p++
          if (c == "\"" || c == "\\" || c == "/") out = out c
          else if (c == "b") out = out "\b"
          else if (c == "f") out = out "\f"
          else if (c == "n") out = out "\n"
          else if (c == "r") out = out "\r"
          else if (c == "t") out = out "\t"
          else if (c == "u") {
            if (p + 3 > len) failparse()
            p += 4
            out = out "\001"
          } else failparse()
          continue
        }
        if (c < " ") failparse()
        out = out c
      }
      failparse()
    }
    function parse_literal(   c) {
      c = substr(text, p, 1)
      if (c == "t") { if (substr(text, p, 4) != "true") failparse(); p += 4; return }
      if (c == "f") { if (substr(text, p, 5) != "false") failparse(); p += 5; return }
      if (c == "n") { if (substr(text, p, 4) != "null") failparse(); p += 4; return }
      if (c == "-" || (c >= "0" && c <= "9")) {
        p++
        while (p <= len && substr(text, p, 1) ~ /[0-9eE+.\-]/) p++
        return
      }
      failparse()
    }
    function parse_array(collect, depth,   element, c) {
      p++
      skip_ws()
      if (substr(text, p, 1) == "]") { p++; return }
      while (1) {
        skip_ws()
        c = substr(text, p, 1)
        if (c == "\"") {
          element = parse_string()
          if (collect == 1 && element ~ /^[0-9a-f]{64}$/) binaries[element] = 1
          if (collect == 2 && element ~ /^[0-9a-f]{64}$/) bundles[element] = 1
        } else parse_value(0, depth)
        skip_ws()
        c = substr(text, p, 1)
        if (c == "]") { p++; return }
        if (c != ",") failparse()
        p++
      }
    }
    function parse_object(depth,   key, collect_next, c) {
      p++
      skip_ws()
      if (substr(text, p, 1) == "}") { p++; return }
      while (1) {
        skip_ws()
        key = parse_string()
        skip_ws()
        if (substr(text, p, 1) != ":") failparse()
        p++
        collect_next = 0
        if (depth == 1 && key == "binaries") collect_next = 1
        if (depth == 1 && key == "bundles") collect_next = 2
        parse_value(collect_next, depth)
        skip_ws()
        c = substr(text, p, 1)
        if (c == "}") { p++; return }
        if (c != ",") failparse()
        p++
      }
    }
    function parse_value(collect, depth,   c) {
      skip_ws()
      c = substr(text, p, 1)
      if (c == "{") { parse_object(depth + 1); return }
      if (c == "[") { parse_array(collect, depth); return }
      if (c == "\"") { parse_string(); return }
      parse_literal()
    }
    { text = text $0 "\n" }
    END {
      if (failed) exit 1
      len = length(text)
      p = 1
      skip_ws()
      if (substr(text, p, 1) != "{") exit 1
      parse_object(1)
      if (failed) exit 1
      skip_ws()
      if (p <= len) exit 1
      if (archive != "" && (archive in bundles)) exit 0
      if (binary != "" && (binary in binaries)) exit 0
      exit 1
    }
  ' "$pin_file"
}
read_channel_index() {
  index_file=$1
  index_platform=$2
  LC_ALL=C awk -v want="$index_platform" '
    function platform(key) { return key ~ /^[a-z0-9]+-[a-z0-9]+$/ }
    function sha(s) { return s ~ /^[0-9a-f]{64}$/ }
    {
      line = $0
      sub(/\r$/, "", line)
      sub(/^[ \t]+/, "", line)
      if (line == "" || line ~ /^#/ || line ~ /^\/\//) next
      n = split(line, f, /[ \t]+/)
      key = f[1]
      if (key == "latest") {
        if (have_latest || n != 2) { failed = 1; next }
        latest = f[2]
        have_latest = 1
        next
      }
      if (key != want && !platform(key)) next
      if (seen[key] || n != 3 || !sha(f[3])) { failed = 1; next }
      seen[key] = 1
      if (key == want) sum = f[3]
    }
    END {
      if (failed || !have_latest || sum == "") exit 1
      printf "%s\n%s\n", latest, sum
    }
  ' "$index_file"
}
stage_bundle_mihari() {
  bundle_archive=$1
  bundle_dest=$2
  bundle_names=$(tar -tzf "$bundle_archive") || return 1
  bundle_count=$(printf '%s\n' "$bundle_names" | grep -c -x mihari || true)
  [ "$bundle_count" -eq 1 ] || return 1
  bundle_kind=$(LC_ALL=C tar -tvzf "$bundle_archive" | LC_ALL=C awk '
    $NF == "mihari" { print substr($1, 1, 1); c++ }
    END { if (c != 1) exit 1 }
  ') || return 1
  [ "$bundle_kind" = "-" ] || return 1
  rm -f "$stage/tar-status"
  (
    set +e
    tar -xOzf "$bundle_archive" mihari
    printf '%s\n' $? > "$stage/tar-status"
  ) | head -c 268435457 > "$bundle_dest" || return 1
  [ "$(wc -c < "$bundle_dest")" -le 268435456 ] || return 1
  [ -f "$stage/tar-status" ] && [ "$(tr -d "[:space:]" < "$stage/tar-status")" -eq 0 ] || return 1
  chmod 0700 "$bundle_dest" || return 1
}
if ! trusted_entry "$entry"; then entry=""; fi
if [ -n "$entry" ] && ! helper_capable "$entry"; then entry=""; fi
# Bundle authorization must run before the offline-helper refusal. An empty
# machine has no helper yet; refusing first would reject a pinned or indexed
# bundle. Copy into this stage before hashing or extracting, and never execute
# the caller archive path. Do not fall back to a GitHub helper.
if [ -z "$entry" ] && [ -n "${bundle:-}" ]; then
  printf '\033[1;34m•\033[0m %s\n' "Staging bundle"
  step_started=$(date +%s)
  [ -f "$bundle" ] && [ ! -L "$bundle" ] || fail "cannot stage install bundle"
  if [ "$os" = linux ]; then bundle_size=$(stat -c %s "$bundle") || fail "cannot stage install bundle"; else bundle_size=$(stat -f %z "$bundle") || fail "cannot stage install bundle"; fi
  [ "$bundle_size" -le 1073741824 ] || fail "install bundle exceeds limit"
  cp "$bundle" "$stage/bundle" || fail "cannot stage install bundle"
  chmod 0600 "$stage/bundle" || fail "cannot stage install bundle"
  step_now=$(date +%s)
  printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
  printf '\033[1;34m•\033[0m %s\n' "Verifying bundle"
  step_started=$(date +%s)
  archive_sha=$(checksum "$stage/bundle") || fail "cannot hash install bundle"
  offline_bundle_pin=0
  if [ "$bootstrap_mode" = online ]; then
    case "$channel" in
      main) index_url=https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt ;;
      dev) index_url=https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt ;;
      *) fail "invalid helper channel" ;;
    esac
    root_fetch_index "$index_url" "$stage/index"
    index_parsed=$(read_channel_index "$stage/index" "$os-$arch") || fail "invalid channel index"
    index_latest=$(printf '%s\n' "$index_parsed" | sed -n '1p')
    index_sum=$(printf '%s\n' "$index_parsed" | sed -n '2p')
    [ "$index_latest" = "$tag" ] || fail "channel index latest does not match release tag"
    [ "$index_sum" = "$archive_sha" ] || fail "install bundle checksum mismatch"
    step_now=$(date +%s)
    printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
  else
    manifest="${install_root:-/usr/local/lib/mihari}/install-trust/manifest.json"
    case "$manifest" in
      /*) ;;
      *) fail "invalid install root" ;;
    esac
    root_path_trusted "$manifest" || fail "Offline installation requires a trusted helper with replacement confirmation support. Prepare a current Mihari installation before installing this offline candidate."
    if manifest_pins_digest "$manifest" "$archive_sha" ""; then
      offline_bundle_pin=1
    fi
    step_now=$(date +%s)
    printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
  fi
  printf '\033[1;34m•\033[0m %s\n' "Extracting bundled installer"
  step_started=$(date +%s)
  stage_bundle_mihari "$stage/bundle" "$stage/mihari" || fail "bundle does not contain mihari"
  binary_sha=$(checksum "$stage/mihari") || fail "cannot hash bundled mihari"
  if [ "$bootstrap_mode" != online ] && [ "$offline_bundle_pin" != 1 ]; then
    manifest_pins_digest "$manifest" "$archive_sha" "$binary_sha" || fail "Offline installation requires a trusted helper with replacement confirmation support. Prepare a current Mihari installation before installing this offline candidate."
  fi
  helper_capable "$stage/mihari" || fail "The current official helper lacks replacement confirmation support. Update the installer helper before installing the selected version."
  entry=$stage/mihari
  candidate=$stage/mihari
  bundle=$stage/bundle
  step_now=$(date +%s)
  printf '  elapsed %d:%02d\n' $(( (step_now - step_started) / 60 )) $(( (step_now - step_started) % 60 ))
fi
# Local offline candidates never cause an implicit network bootstrap. Remote
# AIO passes online explicitly, even though its verified bundle is now local.
if [ "$bootstrap_mode" = offline ] && [ -n "$candidate" ] && [ -z "$entry" ]; then
  fail "Offline installation requires a trusted helper with replacement confirmation support. Prepare a current Mihari installation before installing this offline candidate."
fi
if [ -z "$candidate" ]; then
  verified_binary "$tag" "$stage/candidate" "Downloading release" "Verifying release"
  candidate="$stage/candidate"
fi
if [ -z "${bundle:-}" ] && [ -z "$entry" ]; then
  # Resolve the helper independently; never change the selected candidate tag.
  case "$channel" in
    main) helper_url=https://api.github.com/repos/mihari-proxy/mihari/releases/latest;;
    dev) helper_url='https://api.github.com/repos/mihari-proxy/mihari/releases?per_page=100';;
    *) fail "invalid helper channel";;
  esac
  root_fetch "$helper_url" "$stage/helper-latest"
  [ "$(wc -c < "$stage/helper-latest")" -le 1048576 ] || fail "helper release metadata exceeds limit"
  helper_tag=$(tr '{' '\n' <"$stage/helper-latest" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | LC_ALL=C awk -v channel="$channel" '
    function newer(a,b, x,y,n,i) {
      sub(/^v/,"",a); sub(/^v/,"",b); gsub(/-dev\./,".",a); gsub(/-dev\./,".",b)
      n=split(a,x,"."); split(b,y,".")
      for(i=1;i<=n;i++) { if(length(x[i])!=length(y[i])) return length(x[i])>length(y[i]); if("x"x[i]!="x"y[i]) return "x"x[i]>"x"y[i] }
      return 0
    }
    /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$/ {
      if ((channel=="dev") != ($0 ~ /-dev\./)) next
      if (best=="" || newer($0,best)) best=$0
    }
    END { print best }')
  [ -n "$helper_tag" ] || fail "No current helper release found; prepare a trusted Mihari helper with replacement confirmation support."
  verified_binary "$helper_tag" "$stage/entry" "Downloading installer" "Verifying installer"
  entry="$stage/entry"
  helper_capable "$entry" || fail "The current official helper lacks replacement confirmation support. Update the installer helper before installing the selected version."
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
# BEGIN REPLACEMENT CONFIRMATION
# This deliberately accepts only the bounded, string/object/array subset emitted
# by this helper. Reject unknown fields, duplicate keys and escapes other than
# Go encoding/json HTML escapes. Never evaluate or display unvalidated JSON.
read_confirmation_preview() {
  [ "$(wc -c < "$1")" -le 65536 ] || return 1
  LC_ALL=C awk -v display="${2:-id}" '
  function die() { failed=1; exit 1 }
  function ws() { while (substr(s,p,1)==" ") p++ }
  function take(c) { ws(); if (substr(s,p,1)!=c) die(); p++ }
  function str( out,c,e) {
    take("\""); out=""
    while (p<=length(s)) {
      c=substr(s,p++,1)
      if (c=="\"") return out
      if (c=="\\") {
        e=substr(s,p,5); p+=5
        if (e=="u003e") c=">"; else if (e=="u003c") c="<"; else if (e=="u0026") c="&"; else die()
      }
      if (c !~ /^[ -~]$/) die()
      out=out c
    }
    die()
  }
  function allowed(path,k) {
    if (path=="") return k=="schema" || k=="error"
    if (path=="error") return k=="code" || k=="message" || k=="details"
    if (path=="error.details") return k=="reason" || k=="risk" || k=="targets" || k=="target_version" || k=="preview_id"
    if (path ~ /^error.details.targets\.[0-9]+$/) return k=="roles" || k=="version"
    return 0
  }
  function object(path, k,q,n) {
    take("{"); n=0
    while (1) {
      k=str(); if (!allowed(path,k)) die()
      q=(path=="" ? k : path "." k)
      if (seen[q]++) die()
      take(":"); n++
      if (q=="error" || q=="error.details") object(q)
      else if (k=="targets" || k=="roles") array(q,k)
      else value[q]=str()
      ws(); if (substr(s,p,1)=="}") { p++; break }
      take(",")
    }
    if ((path=="" && n!=2) || (path=="error" && n!=3) || (path=="error.details" && n!=5) || (path ~ /targets\.[0-9]+$/ && n!=2)) die()
  }
  function array(path,kind, n,q,r) {
    take("["); n=0; ws()
    if (substr(s,p,1)=="]") { p++; counts[path]=0; return }
    while (1) {
      n++; if (n>128) die(); q=path "." n
      if (kind=="targets") object(q)
      else { r=str(); if (r!="binary" && r!="service" && r!="path" && r!="managed") die(); value[q]=r }
      ws(); if (substr(s,p,1)=="]") { p++; break }
      take(",")
    }
    counts[path]=n
  }
  function version(v) { return v=="unknown" || v ~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$/ }
  { if (NR!=1 || $0 ~ /[[:cntrl:]]/) die(); s=$0; p=1; object(""); ws(); if (p<=length(s)) die() }
  END {
    if (failed || NR!=1) exit 1
    d="error.details"; id=value[d ".preview_id"]; risk=value[d ".risk"]
    if (value["schema"]!="mihari.error/v1" || value["error.code"]!="invalid_argument" || value[d ".reason"]!="replacement_confirmation_required" || (risk!="downgrade" && risk!="unknown") || length(id)!=64 || id ~ /[^0-9a-f]/ || !version(value[d ".target_version"])) exit 1
    for (i=1;i<=counts[d ".targets"];i++) {
      q=d ".targets." i
      if (!version(value[q ".version"]) || counts[q ".roles"]<1) exit 1
    }
    if (display=="id") { print id; exit }
    if (risk=="unknown") print "Mihari could not determine version compatibility for this replacement."
    print "Older Mihari versions may not support settings, subscriptions, state, or generated files written by the current version. Mihari may fail to start or load data, which can look like data loss. Downgrade is not a supported configuration migration and does not roll back disk state."
    for (i=1;i<=counts[d ".targets"];i++) {
      q=d ".targets." i; roles=""
      for (j=1;j<=counts[q ".roles"];j++) roles=roles (j==1 ? "" : "/") value[q ".roles." j]
      print roles ": " value[q ".version"] " -> " value[d ".target_version"] "."
    }
  }' "$1"
}
confirm_replacement() {
  read_confirmation_preview "$1" warning >&2 || return 1
  # Open the controlling terminal in a subshell so a failed redirection does not
  # terminate the parent POSIX shell. A pipe on stdin never grants consent.
  (
    exec 3<>/dev/tty
    [ -t 3 ] || exit 1
    printf 'Proceed with this replacement? [y/N] ' >&3
    IFS= read -r answer <&3 || exit 1
    case "$answer" in y|Y|yes|YES) exit 0;; *) exit 1;; esac
  ) 2>/dev/null
}
print_result_warnings() {
  [ -s "$1" ] || return 0
  [ "$(wc -c < "$1")" -le 1048576 ] || {
    printf '%s\n' "Warning: a compatibility warning could not be displayed." >&2
    return 0
  }
  LC_ALL=C awk '
  function printable(text, i, c) {
    for (i = 1; i <= length(text); i++) if (substr(text, i, 1) !~ /^[ -~]$/) return 0
    return 1
  }
  function decode(raw, out, i, c, hex) {
    out = ""
    i = 1
    while (i <= length(raw)) {
      c = substr(raw, i, 1)
      if (c != "\\") { out = out c; i++; continue }
      c = substr(raw, i + 1, 1)
      if (c == "\"" || c == "\\" || c == "/") { out = out c; i += 2; continue }
      if (c != "u") return ""
      hex = substr(raw, i + 2, 4)
      if (hex == "003e") c = ">"
      else if (hex == "003c") c = "<"
      else if (hex == "0026") c = "&"
      else return ""
      out = out c
      i += 6
    }
    return out
  }
  function show(raw, msg) {
    msg = decode(raw)
    if (msg == "" || !printable(msg)) print "Warning: a compatibility warning could not be displayed."
    else print "Warning: " msg
  }
  {
    s = $0
    key = "\"warnings\":"
    p = index(s, key)
    if (p == 0) exit
    p += length(key)
    while (substr(s, p, 1) == " ") p++
    if (substr(s, p, 1) != "[") exit
    p++
    depth = 0
    while (p <= length(s)) {
      c = substr(s, p, 1)
      if (c == "\"") {
        q = p + 1
        raw = ""
        while (q <= length(s)) {
          d = substr(s, q, 1)
          if (d == "\\") { raw = raw substr(s, q, 2); q += 2; continue }
          if (d == "\"") break
          raw = raw d
          q++
        }
        rest = substr(s, q + 1)
        sub(/^ */, "", rest)
        if (raw == "message" && substr(rest, 1, 1) == ":" && depth == 1) {
          r = q + 1
          while (substr(s, r, 1) == " ") r++
          r++
          while (substr(s, r, 1) == " ") r++
          if (substr(s, r, 1) == "\"") {
            r++
            val = ""
            while (r <= length(s)) {
              d = substr(s, r, 1)
              if (d == "\\") { val = val substr(s, r, 2); r += 2; continue }
              if (d == "\"") break
              val = val d
              r++
            }
            show(val)
            p = r + 1
            continue
          }
        }
        p = q + 1
        continue
      }
      if (c == "{" || c == "[") depth++
      else if (c == "}" || c == "]") {
        if (c == "]" && depth == 0) break
        if (depth > 0) depth--
      }
      p++
    }
    if (match(s, /"warnings_omitted":[0-9]+/)) {
      num = substr(s, RSTART, RLENGTH)
      sub(/.*:/, "", num)
      if (num + 0 > 0) print "Warning: " num " additional warnings exceeded the collection limit."
    }
  }
  ' "$1" >&2
}
report_install_success() {
  cat "$stage/error.json" >&2
  print_result_warnings "$stage/result.json"
  printf '\033[1;32m•\033[0m %s\n' "Installation complete."
  printf '%s\n' "Run mihari to open the TUI."
}
report_install_failure() {
  printf '\033[1;31merror:\033[0m %s\n' "Installation failed." >&2
  cat "$stage/error.json" >&2
}
apply_progress_label() {
  if [ -n "${bundle:-}" ]; then
    if [ "${bootstrap_mode:-}" = online ]; then
      printf '%s\n' "Step 3/3 Applying installation"
      return 0
    fi
    printf '%s\n' "Step 2/2 Applying installation"
    return 0
  fi
  printf '%s\n' "Applying installation"
}
run_apply_showing_elapsed() {
  apply_label=$1
  shift
  printf '\033[1;34m•\033[0m %s\n' "$apply_label"
  apply_started=$(date +%s)
  "$@" >"$stage/result.json" 2>"$stage/error.json" &
  apply_pid=$!
  printf '\r  elapsed 0:00'
  while kill -0 "$apply_pid" 2>/dev/null; do
    apply_now=$(date +%s)
    apply_elapsed=$((apply_now - apply_started))
    printf '\r  elapsed %d:%02d' $((apply_elapsed / 60)) $((apply_elapsed % 60))
    sleep 0.1
  done
  apply_status=0
  wait "$apply_pid" || apply_status=$?
  apply_now=$(date +%s)
  apply_elapsed=$((apply_now - apply_started))
  printf '\r  elapsed %d:%02d\n' $((apply_elapsed / 60)) $((apply_elapsed % 60))
  return "$apply_status"
}
apply_with_confirmation() {
  status=0
  set -- service apply --request "$stage/request.json" --json
  [ "$explicit_yes" != 1 ] || set -- "$@" --yes
  run_apply_showing_elapsed "$(apply_progress_label)" "$entry" "$@" || status=$?
  if [ "$status" -eq 0 ]; then
    report_install_success
    return 0
  fi
  if [ "$status" -ne 2 ] || [ "$explicit_yes" = 1 ]; then
    report_install_failure
    return "$status"
  fi
  preview_id=$(read_confirmation_preview "$stage/error.json") || fail "Installation requires review; no changes made."
  confirm_replacement "$stage/error.json" || fail "Cancelled; no installation changes made. Use MIHARI_YES=1 to explicitly accept replacement risk."
  status=0
  run_apply_showing_elapsed "$(apply_progress_label)" "$entry" service apply --request "$stage/request.json" --json --yes --expected-preview "$preview_id" || status=$?
  if [ "$status" -eq 0 ]; then
    report_install_success
    return 0
  fi
  report_install_failure
  return "$status"
}
# END REPLACEMENT CONFIRMATION
apply_with_confirmation
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
