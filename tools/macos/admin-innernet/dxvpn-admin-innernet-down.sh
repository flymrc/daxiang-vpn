#!/bin/sh
set -eu

PATH="/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/bin:/bin:/usr/sbin:/sbin"
CONF="${DXVPN_ADMIN_INNERNET_CONF:-/usr/local/etc/dxvpn/wireguard/admin-innernet.conf}"
LOG="${DXVPN_ADMIN_INNERNET_LOG:-/usr/local/var/log/dxvpn/admin-innernet.log}"

mkdir -p "$(dirname "$LOG")"

{
  echo "$(date '+%Y-%m-%d %H:%M:%S') stopping admin-innernet"
  exec /opt/homebrew/bin/bash /opt/homebrew/bin/wg-quick down "$CONF"
} >> "$LOG" 2>&1
