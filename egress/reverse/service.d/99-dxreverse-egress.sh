#!/system/bin/sh
# Magisk service.d entry for the production Android reverse egress data plane.
# Deploy to /data/adb/service.d/99-dxreverse-egress.sh and chmod 700.

BASE=${DXREVERSE_HOME:-/data/adb/dxreverse}
BIN=${DXREVERSE_BIN:-$BASE/bin/dxreverse}
CONFIG=${DXREVERSE_CONFIG:-$BASE/client.yaml}
LOG=${DXREVERSE_LOG:-/data/local/tmp/dxreverse-egress.log}
STOP_LEGACY=${DXREVERSE_STOP_LEGACY:-1}
RESTART_DELAY=${DXREVERSE_RESTART_DELAY:-5}

log() {
    echo "$(date '+%F %T') $*" >> "$LOG"
}

stop_legacy() {
    [ "$STOP_LEGACY" = "1" ] || return 0
    for pattern in '99-dxandroid-egress' 'dxandroid-egress'; do
        pids=$(pgrep -f "$pattern" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            kill $pids 2>/dev/null || true
            log "stopped legacy process pattern=$pattern pids=$pids"
        fi
    done
}

prepare_android_network() {
    settings put global stay_on_while_plugged_in 3 >/dev/null 2>&1 || true
    dumpsys deviceidle disable >/dev/null 2>&1 || true
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
