#!/bin/sh
set -eu

PATH="/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/bin:/bin:/usr/sbin:/sbin"
CONF="${DXVPN_ADMIN_INNERNET_CONF:-/usr/local/etc/dxvpn/wireguard/admin-innernet.conf}"
LOG="${DXVPN_ADMIN_INNERNET_LOG:-/usr/local/var/log/dxvpn/admin-innernet.log}"
IP="${DXVPN_ADMIN_INNERNET_IP:-10.66.0.40}"
MAC_EGRESS_IP="${DXVPN_MAC_EGRESS_IP:-10.66.0.100}"

mkdir -p "$(dirname "$LOG")"

{
  echo "$(date '+%Y-%m-%d %H:%M:%S') starting admin-innernet"
  if ifconfig | grep -q "$IP"; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') admin-innernet already up"
    exit 0
  fi
  if ifconfig | grep -q "$MAC_EGRESS_IP"; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') refusing to start admin-innernet: mac-egress $MAC_EGRESS_IP is already up"
    exit 1
  fi
  exec /opt/homebrew/bin/bash /opt/homebrew/bin/wg-quick up "$CONF"
} >> "$LOG" 2>&1
