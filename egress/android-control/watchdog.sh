#!/system/bin/sh
# dxandroid-control 看门狗:在出口手机本地周期自检并自愈。
#
# 部署目标:/data/adb/dxandroid/watchdog.sh,由 Magisk service.d 在开机后拉起。
# 负责:
#   1. 保证 dxandroid-control(Go SSH 服务)在跑 —— 它用 IP_FREEBIND 绑
#      10.66.0.101:22,无需等隧道就绪即可启动,隧道一通即可连。
#   2. 保证 dxandroid-egress 代理进程在跑,挂了用原启动脚本重拉。
#   3. (可选)每日定时重启,清理长期运行的内存/状态泄漏。
#
# 控制面只对 WireGuard 隧道内网可见(daemon 绑隧道 IP),公网够不到。

BASE=/data/adb/dxandroid
CONTROL_BIN=$BASE/bin/dxandroid-control
INTERVAL=30

EGRESS_NAME=dxandroid-egress
EGRESS_LAUNCH=/data/adb/service.d/99-dxandroid-egress.sh

# 每日定时重启时刻(本地 24h,如 "04");留空 = 不自动重启(默认关闭,出口慎用)。
REBOOT_HOUR=""

LOG=/data/local/tmp/dxandroid-control.log
STAMP=$BASE/.last-reboot-day

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

control_up() { pgrep -f "$CONTROL_BIN" >/dev/null 2>&1; }
egress_up()  { pgrep -f "$EGRESS_NAME"  >/dev/null 2>&1; }

start_control() {
    if [ -x "$CONTROL_BIN" ]; then
        "$CONTROL_BIN" >> "$LOG" 2>&1 &
        log "started dxandroid-control"
    else
        log "WARN $CONTROL_BIN missing or not executable"
    fi
}

start_egress() {
    if [ -x "$EGRESS_LAUNCH" ]; then
        sh "$EGRESS_LAUNCH" >> "$LOG" 2>&1 &
        log "relaunched $EGRESS_NAME via $EGRESS_LAUNCH"
    else
        log "WARN $EGRESS_LAUNCH missing, cannot relaunch egress"
    fi
}

maybe_reboot() {
    [ -n "$REBOOT_HOUR" ] || return 0
    today=$(date '+%F')
    [ "$(date '+%H')" = "$REBOOT_HOUR" ] || return 0
    [ "$(cat "$STAMP" 2>/dev/null)" = "$today" ] && return 0  # 今天已重启过
    echo "$today" > "$STAMP"
    log "scheduled daily reboot (hour=$REBOOT_HOUR)"
    sync; reboot
}

log "watchdog start (interval=${INTERVAL}s, reboot_hour='${REBOOT_HOUR}')"

while true; do
    control_up || start_control
    egress_up  || start_egress
    maybe_reboot
    sleep "$INTERVAL"
done
