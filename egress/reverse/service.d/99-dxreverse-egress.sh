#!/system/bin/sh
# Magisk service.d entry for the production Android reverse egress data plane.
# Deploy to /data/adb/service.d/99-dxreverse-egress.sh and chmod 700.

BASE=${DXREVERSE_HOME:-/data/adb/dxreverse}
BIN=${DXREVERSE_BIN:-$BASE/bin/dxreverse}
CONFIG=${DXREVERSE_CONFIG:-$BASE/client.yaml}
LOG=${DXREVERSE_LOG:-/data/local/tmp/dxreverse-egress.log}
RESTART_DELAY=${DXREVERSE_RESTART_DELAY:-5}
DISABLE_WIFI=${DXREVERSE_DISABLE_WIFI:-1}
TUNE_BUFFERS=${DXREVERSE_TUNE_BUFFERS:-1}

log() {
    echo "$(date '+%F %T') $*" >> "$LOG"
}

stop_legacy() {
    # Android 生产数据面只允许 dxreverse;旧 dxandroid-egress service 会被禁用。
    for script in /data/adb/service.d/99-dxandroid-egress.sh /data/adb/service.d/99-dxandroid-egress.sh.disabled; do
        if [ -f "$script" ] && [ "$script" = "/data/adb/service.d/99-dxandroid-egress.sh" ]; then
            mv "$script" "$script.disabled" 2>/dev/null || chmod 000 "$script" 2>/dev/null || true
            log "disabled legacy service $script"
        fi
    done
    for pattern in '99-dxandroid-egress' 'dxandroid-egress'; do
        pids=$(pgrep -f "$pattern" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            kill $pids 2>/dev/null || true
            log "stopped legacy process pattern=$pattern pids=$pids"
        fi
    done
}

set_sysctl() {
    key=$1
    value=$2
    sysctl -w "$key=$value" >/dev/null 2>&1 && log "sysctl $key=$value" || true
}

prepare_android_network() {
    settings put global stay_on_while_plugged_in 3 >/dev/null 2>&1 || true
    dumpsys deviceidle disable >/dev/null 2>&1 || true
    if [ "$DISABLE_WIFI" = "1" ]; then
        svc wifi disable >/dev/null 2>&1 || true
    fi
    if [ "$TUNE_BUFFERS" = "1" ]; then
        set_sysctl net.core.rmem_max 8388608
        set_sysctl net.core.wmem_max 8388608
        set_sysctl net.ipv4.udp_rmem_min 65536
        set_sysctl net.ipv4.udp_wmem_min 65536
    fi
    route=$(ip route get 1.1.1.1 2>/dev/null || true)
    case "$route" in
        *" dev wlan"*) log "WARN default route is Wi-Fi: $route" ;;
        *) log "default route: $route" ;;
    esac
}

log "dxreverse service.d supervisor starting"
prepare_android_network
stop_legacy

while true; do
    if [ ! -x "$BIN" ]; then
        log "WARN missing executable $BIN"
        sleep "$RESTART_DELAY"
        continue
    fi
    if [ ! -f "$CONFIG" ]; then
        log "WARN missing config $CONFIG"
        sleep "$RESTART_DELAY"
        continue
    fi
    log "starting dxreverse client config=$CONFIG"
    "$BIN" client --config "$CONFIG" >> "$LOG" 2>&1
    rc=$?
    log "dxreverse client exited rc=$rc; restarting in ${RESTART_DELAY}s"
    sleep "$RESTART_DELAY"
done
