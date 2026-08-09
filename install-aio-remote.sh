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
set -eu

# Fixed public direct link to the root index.txt. mihari distribution is fully
# public (signing disabled on the AList drive), so this URL is stable and
# identical across releases — copy-paste, never hand-edit. The release workflow
# uploads index.txt to this exact path each publish.
INDEX_URL="${MIHARI_INDEX_URL:-https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt}"
BUNDLE_URL="${MIHARI_BUNDLE_URL:-}"
YES=0
for arg in "$@"; do
  case "$arg" in
    --yes|-y) YES=1 ;;
    *) ;;
  esac
done

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

# Detect platform (mirrors install.sh).
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

# Downloader: dl writes to a file, fetch writes to stdout (mirrors install.sh).
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO- "$1"; }
else
  err "need curl or wget"
fi

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else err "need sha256sum or shasum"
  fi
}

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
  [ -n "$bundle_url" ] || err "index 未包含本平台 $platform 的包。"
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
else
  info "当前已安装 $current${latest:+，最新版本为 $latest}。"
  confirm "  安装？" || { info "已取消。"; exit 0; }
fi

# Download + verify + extract to a fixed, idempotent work dir.
workdir="${HOME}/Downloads/mihari-aio"
mkdir -p "$workdir"
archive="${workdir}/mihari-all-in-one-${platform}.tar.gz"
info "下载 ${bundle_url} …"
dl "$bundle_url" "$archive" || err "下载失败: $bundle_url"
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
sh "${workdir}/install-aio.sh" "$workdir"
