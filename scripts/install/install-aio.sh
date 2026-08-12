#!/usr/bin/env sh
# mihari all-in-one LOCAL installer (script 2). Offline: lays down the mihari
# binary + bundled mihomo core + GeoIP from <bundle_dir> with zero network.
#   sh install-aio.sh [bundle_dir]      (bundle_dir defaults to this script's dir)
#
# Bundle layout (produced by scripts/build-all-in-one):
#   mihari            -> $BIN_DIR/mihari
#   data/bin/mihomo   -> $MIHARI_DATA/bin/mihomo      (overwrite)
#   data/geoip/*.mmdb -> $MIHARI_DATA/geoip/*.mmdb    (overwrite)
#
# Never touches: mihari.yaml, subscriptions/, control.token, onboarding.json,
# logs/, web/ (user-private config and panel state stay intact).
#
# Environment overrides:
#   MIHARI_BIN    mihari binary install dir (default /usr/local/bin)
#   MIHARI_DATA   data root (default ~/.mihari)
set -eu

bundle_dir="${1:-$(cd "$(dirname "$0")" && pwd)}"

info() { printf '\033[1;34m•\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ -f "$bundle_dir/mihari" ] || err "all-in-one bundle not found at $bundle_dir (expected the mihari binary)"
[ -f "$bundle_dir/data/bin/mihomo" ] || err "bundled mihomo core missing at $bundle_dir/data/bin/mihomo"
[ -f "$bundle_dir/data/geoip/GeoLite2-Country.mmdb" ] || err "bundled GeoIP Country missing at $bundle_dir/data/geoip"
[ -f "$bundle_dir/data/geoip/GeoLite2-ASN.mmdb" ] || err "bundled GeoIP ASN missing at $bundle_dir/data/geoip"

BIN_DIR="${MIHARI_BIN:-/usr/local/bin}"
DATA_DIR="${MIHARI_DATA:-$HOME/.mihari}"
mihari_bin="${BIN_DIR}/mihari"

# Elevate for writes to the system binary dir when needed (mirrors install.sh).
SUDO=""
if [ ! -w "$BIN_DIR" ] 2>/dev/null; then
  if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  fi
fi

# Stop anything holding file locks on the binary / MMDBs before overwriting.
# `service stop` covers the registered-service case (and survives a systemd
# restart policy); pkill covers a foreground daemon. This symmetric handling is
# the gap install.sh leaves open (design §4.4).
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

# 1. mihari binary -> BIN_DIR.
$SUDO mkdir -p "$BIN_DIR"
info "安装 mihari 到 $mihari_bin"
$SUDO install -m 0755 "$bundle_dir/mihari" "$mihari_bin"

# 2. Data overlay -> MIHARI_DATA (bundle is authoritative for core + GeoIP;
#    user config / panel state below is never touched).
mkdir -p "$DATA_DIR/bin" "$DATA_DIR/geoip"
info "覆盖 mihomo 核心与 GeoIP 到 $DATA_DIR"
install -m 0755 "$bundle_dir/data/bin/mihomo" "$DATA_DIR/bin/mihomo"
install -m 0644 "$bundle_dir/data/geoip/GeoLite2-Country.mmdb" "$DATA_DIR/geoip/GeoLite2-Country.mmdb"
install -m 0644 "$bundle_dir/data/geoip/GeoLite2-ASN.mmdb" "$DATA_DIR/geoip/GeoLite2-ASN.mmdb"

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
