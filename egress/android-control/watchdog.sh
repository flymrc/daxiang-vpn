#!/system/bin/sh
# zhandroid-control 看门狗:在出口手机本地周期自检并自愈。
#
# 部署目标:/data/adb/zhandroid/watchdog.sh,由 Magisk service.d 在开机后拉起。
# 负责:
#   1. 保证 zhandroid-control(Go SSH 服务)在跑 —— 隧道 IP 就绪后绑定
#      10.66.0.101:2022,避免 Android 上 IP_FREEBIND/accept4 兼容性问题。
#   2. external 模式下尝试拉起 WireGuard App 隧道。
#   3. 保证 zhreverse 反向出口数据面在跑,挂了用新版启动脚本重拉。
#   4. (可选)每日定时重启,清理长期运行的内存/状态泄漏。
#
# 控制面只对 WireGuard 隧道内网可见(daemon 绑隧道 IP),公网够不到。

BASE=/data/adb/zhandroid
CONTROL_BIN=$BASE/bin/zhandroid-control
# 监听地址:绑隧道 IP。端口 2022 是当前生产控制面端口。
CONTROL_LISTEN=10.66.0.101:2022
WG_IP=10.66.0.101
WG_HUB_IP=10.66.0.1
WG_TUNNEL_NAME=jp-android-01
WG_INTENT_COOLDOWN=120
INTERVAL=30
NETWORK_TUNE_INTERVAL=300
DISABLE_WIFI=1
TUNE_BUFFERS=1

EGRESS_NAME=zhreverse
EGRESS_LAUNCH=/data/adb/service.d/99-zhreverse-egress.sh

# 每日定时重启时刻(本地 24h,如 "04");留空 = 不自动重启(默认关闭,出口慎用)。
REBOOT_HOUR=""

LOG=/data/local/tmp/zhandroid-control.log
STAMP=$BASE/.last-reboot-day
WG_LAST_INTENT_FILE=$BASE/.last-wg-intent
NETWORK_LAST_TUNE_FILE=$BASE/.last-network-tune

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

control_up() {
    pgrep -f "$CONTROL_BIN" >/dev/null 2>&1 || return 1
    # 10.66.0.101:2022 in /proc/net/tcp little endian, state 0A = LISTEN.
    grep -qi '6500420A:07E6 .* 0A ' /proc/net/tcp 2>/dev/null
}
# 看的是新版 reverse egress 监督脚本(99-zhreverse-egress.sh,自带 while 循环保活 binary)
# 在不在,而不是 binary 本身——避免在 binary 短暂缺失时重复拉起多个监督循环。
egress_up()  { pgrep -f 99-zhreverse-egress >/dev/null 2>&1; }
wg_addr_up() { ip addr 2>/dev/null | grep -q "$WG_IP/"; }
wg_hub_reachable() { ping -c 1 -W 2 "$WG_HUB_IP" >/dev/null 2>&1; }

set_sysctl() {
    key=$1
    value=$2
    sysctl -w "$key=$value" >/dev/null 2>&1 && log "sysctl $key=$value" || true
}

disable_legacy_egress() {
    legacy=/data/adb/service.d/99-zhandroid-egress.sh
    if [ -f "$legacy" ]; then
        mv "$legacy" "$legacy.disabled" 2>/dev/null || chmod 000 "$legacy" 2>/dev/null || true
        log "disabled legacy service $legacy"
    fi
    for pattern in '99-zhandroid-egress' 'zhandroid-egress'; do
        pids=$(pgrep -f "$pattern" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            kill $pids 2>/dev/null || true
            log "stopped legacy process pattern=$pattern pids=$pids"
        fi
    done
}

ensure_network_baseline() {
    now=$(date +%s)
    last=$(cat "$NETWORK_LAST_TUNE_FILE" 2>/dev/null || echo 0)
    if [ $((now - last)) -lt "$NETWORK_TUNE_INTERVAL" ]; then
        return 0
    fi
    echo "$now" > "$NETWORK_LAST_TUNE_FILE"

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
    esac
    disable_legacy_egress
}

start_control() {
    if ! wg_addr_up; then
        log "control deferred; $WG_IP not present yet"
        return 0
    fi
    if [ -x "$CONTROL_BIN" ]; then
        pids=$(pgrep -f "$CONTROL_BIN" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            kill $pids 2>/dev/null || true
            log "stopped stale zhandroid-control pids=$pids"
            sleep 1
        fi
        "$CONTROL_BIN" -listen "$CONTROL_LISTEN" -freebind=false >> "$LOG" 2>&1 &
        log "started zhandroid-control on $CONTROL_LISTEN"
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
        # WireGuard App teardown can lag a few seconds. If UP is sent while the
        # old tun0 is still present, Android may leave a tunnel with an address
        # but no fresh Hub handshake.
        for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
            wg_addr_up || break
            sleep 1
        done
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
    ensure_network_baseline
    ensure_wg
    control_up || start_control
    egress_up  || start_egress
    maybe_reboot
    sleep "$INTERVAL"
done
