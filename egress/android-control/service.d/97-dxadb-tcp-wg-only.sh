#!/system/bin/sh
# Magisk service.d entry: enable TCP ADB only on the WireGuard control plane.
# Deploy to /data/adb/service.d/97-dxadb-tcp-wg-only.sh and chmod 700.

PORT=${DXADB_TCP_PORT:-5555}
WG_IF=${DXADB_WG_IF:-tun0}
WG_CIDR=${DXADB_WG_CIDR:-10.66.0.0/24}
LOG=${DXADB_LOG:-/data/local/tmp/dxadb-tcp.log}

log() {
    echo "$(date '+%F %T') $*" >> "$LOG"
}

run() {
    "$@" >> "$LOG" 2>&1 || log "WARN command failed: $*"
}

install_iptables() {
    if command -v iptables >/dev/null 2>&1; then
        iptables -N DXADB_TCP 2>/dev/null || true
        iptables -F DXADB_TCP 2>/dev/null || true
        iptables -A DXADB_TCP -i "$WG_IF" -s "$WG_CIDR" -p tcp --dport "$PORT" -j ACCEPT 2>/dev/null || true
        iptables -A DXADB_TCP -p tcp --dport "$PORT" -j DROP 2>/dev/null || true
        iptables -C INPUT -p tcp --dport "$PORT" -j DXADB_TCP 2>/dev/null || \
            iptables -I INPUT 1 -p tcp --dport "$PORT" -j DXADB_TCP 2>/dev/null || true
    fi
    if command -v ip6tables >/dev/null 2>&1; then
        ip6tables -N DXADB_TCP6 2>/dev/null || true
        ip6tables -F DXADB_TCP6 2>/dev/null || true
        ip6tables -A DXADB_TCP6 -p tcp --dport "$PORT" -j DROP 2>/dev/null || true
        ip6tables -C INPUT -p tcp --dport "$PORT" -j DXADB_TCP6 2>/dev/null || \
            ip6tables -I INPUT 1 -p tcp --dport "$PORT" -j DXADB_TCP6 2>/dev/null || true
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
