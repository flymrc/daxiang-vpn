#!/system/bin/sh
# dxandroid-control 看门狗:在出口手机本地周期自检并自愈。
#
# 部署目标:/data/adb/dxandroid/watchdog.sh,由 Magisk service.d 在开机后拉起。
# 负责三件事:
#   1. 等 WireGuard 隧道(tun0 / 10.66.0.101)就绪。
#   2. 保证 dropbear SSH 守护进程在 10.66.0.101:22 上跑(仅密钥登录)。
#   3. 保证 dxandroid-egress 代理进程在跑,挂了就用原启动脚本重拉。
#
# 设计取舍:
#   - 只绑定 WireGuard 内网 IP,公网够不到,只有隧道内 peer(Hub)能连。
#   - external 模式下 WireGuard 隧道由 WireGuard App 拥有,shell 难以可靠重建;
#     这里只检测并告警,真正修复见 README 的「WG 隧道自愈」TODO。

WG_IP=10.66.0.101
SSH_PORT=22
INTERVAL=30

BASE=/data/adb/dxandroid
BIN=$BASE/bin
KEYS=$BASE/keys
DROPBEAR=$BIN/dropbear
DROPBEARKEY=$BIN/dropbearkey
HOSTKEY=$KEYS/dropbear_ed25519_host_key
AUTHKEYS=$BASE/authorized_keys

EGRESS_NAME=dxandroid-egress
EGRESS_LAUNCH=/data/adb/service.d/99-dxandroid-egress.sh

LOG=/data/local/tmp/dxandroid-control.log

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

wg_up()        { ip addr show tun0 2>/dev/null | grep -q "$WG_IP"; }
dropbear_up()  { pgrep -f "$DROPBEAR" >/dev/null 2>&1; }
egress_up()    { pgrep -f "$EGRESS_NAME" >/dev/null 2>&1; }

ensure_hostkey() {
    [ -d "$KEYS" ] || mkdir -p "$KEYS"
    if [ ! -f "$HOSTKEY" ]; then
        "$DROPBEARKEY" -t ed25519 -f "$HOSTKEY" >> "$LOG" 2>&1 \
            && log "generated dropbear host key"
    fi
}

start_dropbear() {
    # -s 禁止密码登录;-g 禁止 root 用密码;-E 日志到 stderr;-p 仅绑 WG IP。
    # 授权公钥取自 $AUTHKEYS(见 README 的 -U/HOME 说明,按设备实测确定路径)。
    HOME="$BASE" "$DROPBEAR" -p "${WG_IP}:${SSH_PORT}" -r "$HOSTKEY" -s -g -E >> "$LOG" 2>&1 &
    log "started dropbear on ${WG_IP}:${SSH_PORT}"
}

start_egress() {
    if [ -x "$EGRESS_LAUNCH" ]; then
        sh "$EGRESS_LAUNCH" >> "$LOG" 2>&1 &
        log "relaunched $EGRESS_NAME via $EGRESS_LAUNCH"
    else
        log "WARN $EGRESS_LAUNCH missing, cannot relaunch egress"
    fi
}

log "watchdog start (interval=${INTERVAL}s)"
ensure_hostkey

while true; do
    if wg_up; then
        dropbear_up || start_dropbear
        egress_up   || start_egress
    else
        log "WARN wg tunnel down (no $WG_IP on tun0) — control unreachable until tunnel recovers"
        # TODO: external 模式下尝试触发 WireGuard App 重连(见 README)。
    fi
    sleep "$INTERVAL"
done
