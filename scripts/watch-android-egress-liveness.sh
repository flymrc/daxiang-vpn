#!/usr/bin/env bash
# Android 出口被动存活探针 —— 在 Hub 上运行。
#
# 原理:WireGuard 的 keepalive(25s)不会刷新 `latest handshake` 时间戳,
# 但会让对端的 RX 字节持续增长。所以判断 Android 出口是否在线,看
# `wg show <wg> transfer` 里 Android peer 的 RX 是否在涨,比看握手时间更灵敏。
#
# 纯被动:只读 wg 计数,不向手机发包。掉线(RX 连续不涨)时触发告警钩子。
#
# 用法(在 Hub 上):
#   ./watch-android-egress-liveness.sh
#   INTERVAL=25 STALL_CYCLES=4 ANDROID_IP=10.66.0.101 ./watch-android-egress-liveness.sh
# 长期运行建议挂 systemd / tmux。

set -euo pipefail

WG=${WG:-wg0}
ANDROID_IP=${ANDROID_IP:-10.66.0.101}
INTERVAL=${INTERVAL:-30}        # 采样周期(秒);keepalive 是 25s,取 ≥25
STALL_CYCLES=${STALL_CYCLES:-3} # 连续多少个周期 RX 不涨判定掉线

ts() { date '+%F %T'; }

# 告警钩子:替换成实际通知(webhook / 邮件 / 微信等)。
alert() {
    echo "$(ts) ALERT: $*"
    # 例:curl -fsS -m 10 -X POST "$ZH_ALERT_WEBHOOK" -d "android-egress down: $*" || true
}

peer=$(wg show "$WG" allowed-ips | awk -v ip="${ANDROID_IP}/32" '$2==ip{print $1}')
if [ -z "$peer" ]; then
    echo "$(ts) FATAL: 在 $WG 上找不到 $ANDROID_IP 对应的 peer" >&2
    exit 1
fi

rx_of() { wg show "$WG" transfer | awk -v k="$peer" '$1==k{print $2}'; }

prev=$(rx_of); prev=${prev:-0}
stall=0
echo "$(ts) watching $ANDROID_IP (peer ${peer:0:12}…) interval=${INTERVAL}s rx0=$prev"

while true; do
    sleep "$INTERVAL"
    cur=$(rx_of); cur=${cur:-0}
    if [ "$cur" -gt "$prev" ]; then
        stall=0
        echo "$(ts) ALIVE rx=$cur (+$((cur - prev)))"
    else
        stall=$((stall + 1))
        echo "$(ts) NO-RX cycles=$stall rx=$cur"
        if [ "$stall" -eq "$STALL_CYCLES" ]; then
            alert "$ANDROID_IP 已静默 $((stall * INTERVAL))s(RX 未增长)"
        fi
    fi
    prev=$cur
done
