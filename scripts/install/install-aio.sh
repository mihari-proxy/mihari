#!/usr/bin/env sh
# mihari all-in-one LOCAL installer (script 2). Offline: lays down the mihari
# binary + bundled mihomo core + GeoIP from <bundle_dir> with zero network.
#   sh install-aio.sh [bundle_dir]      (bundle_dir defaults to this script's dir)
#
# Bundle layout (produced by scripts/build-all-in-one):
#   mihari                 -> $BIN_DIR/mihari
#   data/bin/mihomo        -> $MIHARI_DATA/bin/mihomo         (overwrite)
#   data/bin/core-channel  -> $MIHARI_DATA/bin/core-channel   (overwrite if present)
#   data/geoip/*.mmdb      -> $MIHARI_DATA/geoip/*.mmdb       (overwrite)
#
# Never touches: mihari.yaml, subscriptions/, control.token, onboarding.json,
# logs/, web/ (user-private config and panel state stay intact).
#
# Environment overrides:
#   MIHARI_BIN    mihari binary install dir (default /usr/local/bin)
#   MIHARI_DATA   data root (default ~/.mihari)
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

[ -f "$bundle_dir/mihari" ] || err "all-in-one bundle not found at $bundle_dir (expected the mihari binary)"
[ -f "$bundle_dir/data/bin/mihomo" ] || err "bundled mihomo core missing at $bundle_dir/data/bin/mihomo"
[ -f "$bundle_dir/data/geoip/GeoLite2-Country.mmdb" ] || err "bundled GeoIP Country missing at $bundle_dir/data/geoip"
[ -f "$bundle_dir/data/geoip/GeoLite2-ASN.mmdb" ] || err "bundled GeoIP ASN missing at $bundle_dir/data/geoip"

BIN_DIR="${MIHARI_BIN:-/usr/local/bin}"
DATA_DIR="${MIHARI_DATA:-$HOME/.mihari}"
mihari_bin="${BIN_DIR}/mihari"

channel_data_root() {
  if [ -n "${MIHARI_DATA:-}" ]; then
    printf '%s\n' "$MIHARI_DATA"
    return
  fi
  if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ]; then
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
    printf '%s\n' "$home/.mihari"
    return
  fi
  printf '%s\n' "${HOME}/.mihari"
}

write_mihari_channel() {
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
      chown "$uid:$gid" "$root" || true
    fi
    chown "$uid:$gid" "$root/mihari-channel" || true
  fi
}

# Elevate for writes to the system binary dir when needed (mirrors install.sh).
SUDO=""
if [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ] && [ ! -w "$BIN_DIR" ] 2>/dev/null; then
  if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  fi
fi

# Stop anything holding file locks on the binary / MMDBs before overwriting.
# `service stop` covers the registered-service case (and survives a systemd
# restart policy); pkill covers a foreground daemon. This symmetric handling is
# the gap install.sh leaves open (design §4.4).
if [ "${MIHARI_INSTALL_TEST_MODE:-}" != "1" ]; then
  if [ -x "$mihari_bin" ]; then
    $SUDO "$mihari_bin" service stop >/dev/null 2>&1 || true
  fi
  if command -v pgrep >/dev/null 2>&1 && pgrep -x mihari >/dev/null 2>&1; then
    info "检测到运行中的 mihari，停止以释放文件锁…"
    pkill -x mihari >/dev/null 2>&1 || true
    tries=0
    while [ "$tries" -lt 5 ] && pgrep -x mihari >/dev/null 2>&1; do
      tries=$((tries + 1))
      sleep 1
    done
  fi
fi

# 1. mihari binary -> BIN_DIR.
$SUDO mkdir -p "$BIN_DIR"
info "安装 mihari 到 $mihari_bin"
$SUDO install -m 0755 "$bundle_dir/mihari" "$mihari_bin"

# 2. Data overlay -> MIHARI_DATA (bundle is authoritative for core + GeoIP;
#    user config / panel state below is never touched).
mkdir -p "$DATA_DIR/bin" "$DATA_DIR/geoip"
info "覆盖 mihomo 核心与 GeoIP 到 $DATA_DIR"
install -m 0755 "$bundle_dir/data/bin/mihomo" "$DATA_DIR/bin/mihomo"
if [ -f "$bundle_dir/data/bin/core-channel" ]; then
  install -m 0644 "$bundle_dir/data/bin/core-channel" "$DATA_DIR/bin/core-channel"
fi
install -m 0644 "$bundle_dir/data/geoip/GeoLite2-Country.mmdb" "$DATA_DIR/geoip/GeoLite2-Country.mmdb"
install -m 0644 "$bundle_dir/data/geoip/GeoLite2-ASN.mmdb" "$DATA_DIR/geoip/GeoLite2-ASN.mmdb"

if [ "$CHANNEL_EXPLICIT" -eq 1 ]; then
  write_mihari_channel "$CHANNEL"
fi

if [ "${MIHARI_INSTALL_TEST_MODE:-}" = "1" ]; then
  exit 0
fi

# 3. Service: reinstall when registered (re-stages the service copy from the
#    freshly installed PATH binary, closing the "service vs PATH" version drift),
#    install when fresh. Service control needs root; elevate the step (mirrors
#    install.sh). service status returns "not_installed" for a fresh machine with
#    no elevation, so the branch is reliable; any ambiguity falls through to the
#    fresh-install path whose install/start is the safe default.
status="$($SUDO "$mihari_bin" service status 2>/dev/null || true)"
case "$status" in
  running|stopped)
    info "已注册服务，执行 service reinstall 同步新版本…"
    $SUDO "$mihari_bin" service reinstall
    ;;
  *)
    info "注册并启动服务…"
    $SUDO "$mihari_bin" service install
    $SUDO "$mihari_bin" service start
    ;;
esac

printf '\n\033[1;32m✅ aio 版安装完成！请重启终端，然后运行 mihari 开始使用。\033[0m\n'
