#!/system/bin/sh
# dxandroid-control 看门狗:在出口手机本地周期自检并自愈。
#
# 部署目标:/data/adb/dxandroid/watchdog.sh,由 Magisk service.d 在开机后拉起。
# 负责:
#   1. 保证 dxandroid-control(Go SSH 服务)在跑 —— 它用 IP_FREEBIND 绑
#      10.66.0.101:2022,无需等隧道就绪即可启动,隧道一通即可连。
#   2. external 模式下尝试拉起 WireGuard App 隧道。
#   3. 保证 dxandroid-egress 代理进程在跑,挂了用原启动脚本重拉。
#   4. (可选)每日定时重启,清理长期运行的内存/状态泄漏。
#
# 控制面只对 WireGuard 隧道内网可见(daemon 绑隧道 IP),公网够不到。

BASE=/data/adb/dxandroid
CONTROL_BIN=$BASE/bin/dxandroid-control
# 监听地址:绑隧道 IP。端口 2022 是当前生产控制面端口。
CONTROL_LISTEN=10.66.0.101:2022
WG_IP=10.66.0.101
WG_HUB_IP=10.66.0.1
WG_TUNNEL_NAME=jp-android-01
WG_INTENT_COOLDOWN=120
INTERVAL=30

EGRESS_NAME=dxandroid-egress
EGRESS_LAUNCH=/data/adb/service.d/99-dxandroid-egress.sh

# 每日定时重启时刻(本地 24h,如 "04");留空 = 不自动重启(默认关闭,出口慎用)。
REBOOT_HOUR=""

LOG=/data/local/tmp/dxandroid-control.log
STAMP=$BASE/.last-reboot-day
WG_LAST_INTENT_FILE=$BASE/.last-wg-intent

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

control_up() { pgrep -f "$CONTROL_BIN" >/dev/null 2>&1; }
# 看的是 egress 监督脚本(99-dxandroid-egress.sh,自带 while 循环保活 binary)
# 在不在,而不是 binary 本身——避免在 binary 短暂缺失时重复拉起多个监督循环。
egress_up()  { pgrep -f 99-dxandroid-egress >/dev/null 2>&1; }
wg_addr_up() { ip addr 2>/dev/null | grep -q "$WG_IP/"; }
wg_hub_reachable() { ping -c 1 -W 2 "$WG_HUB_IP" >/dev/null 2>&1; }

start_control() {
    if [ -x "$CONTROL_BIN" ]; then
        "$CONTROL_BIN" -listen "$CONTROL_LISTEN" >> "$LOG" 2>&1 &
        log "started dxandroid-control on $CONTROL_LISTEN"
    else
        log "WARN $CONTROL_BIN missing or not executable"
    fi
}

start_egress() {
    # 用 [ -f ] + sh 执行,不依赖脚本的可执行位(adb push 常丢 +x,曾导致重启后
    # Magisk 与本看门狗都拉不起 egress)。setsid 脱离会话常驻。
    if [ -f "$EGRESS_LAUNCH" ]; then
        setsid sh "$EGRESS_LAUNCH" >/dev/null 2>&1 </dev/null &
        log "relaunched egress supervisor via $EGRESS_LAUNCH"
    else
        log "WARN $EGRESS_LAUNCH missing, cannot relaunch egress"
    fi
}

send_wg_intent() {
    action=$1
    log "wireguard unhealthy; requesting tunnel $action via WireGuard App intent (tunnel=$WG_TUNNEL_NAME)"
    am broadcast \
        -a "com.wireguard.android.action.SET_TUNNEL_$action" \
        -n 'com.wireguard.android/.model.TunnelManager$IntentReceiver' \
        -e tunnel "$WG_TUNNEL_NAME" >> "$LOG" 2>&1 || \
        log "WARN WireGuard App SET_TUNNEL_$action intent failed"
}

recover_wg() {
    mode=$1
    now=$(date +%s)
    last=$(cat "$WG_LAST_INTENT_FILE" 2>/dev/null || echo 0)
    if [ $((now - last)) -lt "$WG_INTENT_COOLDOWN" ]; then
        return 0
    fi

    echo "$now" > "$WG_LAST_INTENT_FILE"
    if [ "$mode" = "bounce" ]; then
        send_wg_intent DOWN
        sleep 3
    fi
    send_wg_intent UP
}

ensure_wg() {
    if ! wg_addr_up; then
        recover_wg up
        return 0
    fi
    if wg_hub_reachable; then
        return 0
    fi
    recover_wg bounce
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
    ensure_wg
    control_up || start_control
    egress_up  || start_egress
    maybe_reboot
    sleep "$INTERVAL"
done
