#!/usr/bin/env sh
# Check the production Android reverse egress path from the Hub side.

set -eu

PROXY=${PROXY:-http://127.0.0.1:18081}
TEST_URL=${TEST_URL:-https://api.ipify.org}
TIMEOUT=${TIMEOUT:-20}

echo "proxy=$PROXY"

ip=$(curl -sS -L --connect-timeout 8 --max-time "$TIMEOUT" --proxy "$PROXY" "$TEST_URL" || true)
case "$ip" in
    *.*.*.*)
        echo "PASS egress_ip=$ip"
        ;;
    *)
        echo "FAIL no egress ip from reverse proxy: $ip"
        exit 1
        ;;
esac

curl -sS -L --connect-timeout 8 --max-time "$TIMEOUT" -o /dev/null \
    -w 'download_probe code=%{http_code} bytes=%{size_download} Bps=%{speed_download} seconds=%{time_total}\n' \
    --proxy "$PROXY" \
    'https://speed.cloudflare.com/__down?bytes=1000000'
