#!/system/bin/sh
# Magisk service.d entry: enable TCP ADB only on the WireGuard control plane.
# Deploy to /data/adb/service.d/97-zhadb-tcp-wg-only.sh and chmod 700.

PORT=${ZHADB_TCP_PORT:-5555}
WG_IF=${ZHADB_WG_IF:-tun0}
WG_CIDR=${ZHADB_WG_CIDR:-10.66.0.0/24}
LOG=${ZHADB_LOG:-/data/local/tmp/zhadb-tcp.log}

log() {
    echo "$(date '+%F %T') $*" >> "$LOG"
}

run() {
    "$@" >> "$LOG" 2>&1 || log "WARN command failed: $*"
}

install_iptables() {
    if command -v iptables >/dev/null 2>&1; then
        iptables -N ZHADB_TCP 2>/dev/null || true
        iptables -F ZHADB_TCP 2>/dev/null || true
        iptables -A ZHADB_TCP -i "$WG_IF" -s "$WG_CIDR" -p tcp --dport "$PORT" -j ACCEPT 2>/dev/null || true
        iptables -A ZHADB_TCP -p tcp --dport "$PORT" -j DROP 2>/dev/null || true
        iptables -C INPUT -p tcp --dport "$PORT" -j ZHADB_TCP 2>/dev/null || \
            iptables -I INPUT 1 -p tcp --dport "$PORT" -j ZHADB_TCP 2>/dev/null || true
    fi
    if command -v ip6tables >/dev/null 2>&1; then
        ip6tables -N ZHADB_TCP6 2>/dev/null || true
        ip6tables -F ZHADB_TCP6 2>/dev/null || true
        ip6tables -A ZHADB_TCP6 -p tcp --dport "$PORT" -j DROP 2>/dev/null || true
        ip6tables -C INPUT -p tcp --dport "$PORT" -j ZHADB_TCP6 2>/dev/null || \
            ip6tables -I INPUT 1 -p tcp --dport "$PORT" -j ZHADB_TCP6 2>/dev/null || true
    fi
}

log "enabling TCP ADB port=$PORT allowed=${WG_IF}:${WG_CIDR}"
install_iptables

setprop service.adb.tcp.port "$PORT"
run stop adbd
sleep 1
run start adbd
sleep 2
install_iptables

ss -lntup 2>/dev/null | grep ":$PORT" >> "$LOG" 2>&1 || true
log "TCP ADB guard installed"
