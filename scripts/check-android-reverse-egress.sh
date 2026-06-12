#!/usr/bin/env sh
# Check the production Android reverse egress path from the Hub side.
#
# 生产主路径是 IPv6(手机 address_family: ipv6,乐天 F5 的 v4 侧高故障):
# 拿不到 v6 出口 = FAIL。v4-only 目标走手机 v4,时好时坏是常态,失败只
# WARN;但 v4 出口若等于 Hub VPS IP,说明 hub-fallback 回归(2026-06-11
# 起 Hub 不得作为出口兜底),按 FAIL 处理。

set -eu

PROXY=${PROXY:-http://10.66.0.1:18081}
V6_URL=${V6_URL:-https://api6.ipify.org}
V4_URL=${V4_URL:-https://api.ipify.org}
HUB_IP=${HUB_IP:-36.50.84.68}
TIMEOUT=${TIMEOUT:-20}

echo "proxy=$PROXY"

fail=0

ip6=$(curl -sS -L --connect-timeout 8 --max-time "$TIMEOUT" --proxy "$PROXY" "$V6_URL" || true)
case "$ip6" in
    240b:*)
        echo "PASS v6 egress_ip=$ip6 (Rakuten)"
        ;;
    *:*)
        echo "PASS v6 egress_ip=$ip6 (non-Rakuten prefix, check SIM/carrier)"
        ;;
    *)
        echo "FAIL v6 egress (main path) unavailable: '$ip6'"
        fail=1
        ;;
esac

ip4=$(curl -sS -L --connect-timeout 8 --max-time "$TIMEOUT" --proxy "$PROXY" "$V4_URL" || true)
case "$ip4" in
    "$HUB_IP")
        echo "FAIL v4 egress is Hub VPS $HUB_IP: hub-fallback regression"
        fail=1
        ;;
    *.*.*.*)
        echo "PASS v4 egress_ip=$ip4 (phone v4, flaky by nature)"
        ;;
    *)
        echo "WARN v4 egress unavailable: '$ip4' (F5 v4 bad window; v4-only sites degraded)"
        ;;
esac

curl -sS -L --connect-timeout 8 --max-time "$TIMEOUT" -o /dev/null \
    -w 'download_probe code=%{http_code} bytes=%{size_download} Bps=%{speed_download} seconds=%{time_total}\n' \
    --proxy "$PROXY" \
    'https://speed.cloudflare.com/__down?bytes=1000000' \
    || { echo "FAIL download probe"; fail=1; }

exit "$fail"
