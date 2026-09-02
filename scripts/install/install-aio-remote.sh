#!/usr/bin/env sh
# mihari all-in-one REMOTE downloader (script 3). Fetches the platform bundle
# from the AList drive (index.txt → public direct link + sha256), verifies it,
# extracts, and hands off to the local installer (script 2) inside the bundle.
#   curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
#   sh install-aio-remote.sh [--yes|-y]
#
# One-time: only the first offline install goes through this downloader; later
# reinstalls run script 2 (install-aio.sh) directly from an existing bundle.
#
# Environment overrides:
#   MIHARI_INDEX_URL   index.txt public direct link (default: the fixed public URL below)
#   MIHARI_BUNDLE_URL  explicit bundle URL (skips index + sha256 — trust borne by user)
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
  workdir="${HOME}/Downloads/mihari-aio"
  if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
    handoff="sh \"${workdir}/install-aio.sh\" --channel \"${CHANNEL}\" \"${workdir}\""
  else
    handoff="sh \"${workdir}/install-aio.sh\" \"${workdir}\""
  fi
  printf 'CHANNEL=%s\n' "$CHANNEL"
  printf 'EXPLICIT=%s\n' "$CHANNEL_EXPLICIT"
  printf 'INDEX_URL=%s\n' "$INDEX_URL"
  printf 'HANDOFF=%s\n' "$handoff"
  printf 'LATEST=%s\n' "${latest:-}"
}

is_canonical_stable() {
  printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
}

is_canonical_dev() {
  printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$'
}

looks_like_tag() {
  is_canonical_stable "$1" || is_canonical_dev "$1" || printf '%s' "$1" | grep -Eq '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-dev\.(0|[1-9][0-9]*))?$'
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
  [ -n "$index" ] || err "尚未发布完成：无法获取 index（请稍后重试，或检查网络/网盘可用性）。"
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
  [ -n "$latest" ] || err "尚未发布完成：index 无 latest 版本（可能正在发布或已撤回）。"
  if [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ]; then
    [ -n "$bundle_url" ] || err "index 未包含本平台 $platform 的包。"
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

# Version judgment: PATH mihari only, local (no daemon). Single source of truth
# vs index.latest; empty → unknown. (Comparison is equality-only: == latest →
# "reinstall", != latest → "upgrade" with honest versions shown. Full semver is
# not needed since judgment only informs the prompt, never gates the install.)
have_mihari=0
current=""
if command -v mihari >/dev/null 2>&1; then
  have_mihari=1
  current="$(mihari self version --json 2>/dev/null | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)"
fi

if [ "$have_mihari" = "0" ]; then
  info "未检测到 mihari，安装最新版${latest:+ ($latest)}…"
elif [ -z "$current" ]; then
  info "检测到 mihari 但版本未知（二进制可能损坏）。"
  confirm "  重新安装（修复）？" || { info "已取消。"; exit 0; }
elif [ -n "$latest" ] && [ "$current" = "$latest" ]; then
  info "已是最新版本 ($current)。"
  confirm "  重新安装（用于修复）？" || { info "已取消。"; exit 0; }
elif looks_like_tag "$current" && looks_like_tag "$latest" && [ "$(tag_cmp "$current" "$latest")" = "1" ]; then
  info "Installing older Mihari ${latest} over ${current}."
  info "Settings, subscriptions, and generated files from the current version may be unsupported, fail to load, or look like they disappeared."
  info "Downgrade is not a supported config migration and does not roll disk state back."
  confirm "  Continue with downgrade?" || { info "已取消。"; exit 0; }
else
  info "当前已安装 $current${latest:+，最新版本为 $latest}。"
  confirm "  安装？" || { info "已取消。"; exit 0; }
fi

# Download + verify + extract to a fixed, idempotent work dir.
workdir="${HOME}/Downloads/mihari-aio"
mkdir -p "$workdir"
archive="${workdir}/mihari-all-in-one-${platform}.tar.gz"
info "下载 ${bundle_url} …"
download_file_with_progress "$bundle_url" "$archive" || err "下载失败: $bundle_url"
if [ -n "$want_sum" ]; then
  got="$(checksum "$archive")"
  [ "$got" = "$want_sum" ] || err "sha256 校验失败：期望 $want_sum，实际 $got。"
  info "sha256 校验通过。"
fi
info "解压到 ${workdir} …"
tar -xzf "$archive" -C "$workdir" || err "解压失败。"
rm -f "$archive"

# Hand off to the local installer inside the bundle (script 2), passing the
# bundle dir so it locates mihari + data/ without relying on the caller's path.
[ -f "${workdir}/install-aio.sh" ] || err "包内缺少 install-aio.sh。"
if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
  sh "${workdir}/install-aio.sh" --channel "$CHANNEL" "$workdir"
else
  sh "${workdir}/install-aio.sh" "$workdir"
fi
