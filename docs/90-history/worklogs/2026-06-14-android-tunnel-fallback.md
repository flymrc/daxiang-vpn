# 2026-06-14 Android tunnel fallback

## Goal

Keep the successful dual-network split, but avoid breaking the proxy when home
WiFi or broadband disappears.

Desired behavior:

```text
Android -> Hub tunnel:  wlan0 primary, rmnet1 fallback
Android -> target:      rmnet1 only
```

## Implementation

- Added client options:
  - `tunnel_fallback_interface`
  - `tunnel_fallback_after_failures`
  - `tunnel_primary_retry_interval`
- `zhreverse client` now shares a tunnel bind controller across parallel tunnel
  sessions.
- The controller uses the primary interface until it sees enough consecutive
  primary tunnel dial/auth failures.
- While fallback is active, it uses the fallback interface and periodically
  probes the primary interface.
- A successful primary connection exits fallback mode.
- Fallback is TCP/yamux-only for now, matching the current production tunnel.
- Target website TCP and DNS continue to use `target_bind_interface` and are
  not affected by tunnel fallback.

Planned production config:

```yaml
tunnel_bind_interface: wlan0
tunnel_fallback_interface: rmnet1
tunnel_fallback_after_failures: 3
tunnel_primary_retry_interval: 1m
target_bind_interface: rmnet1
```

## Tests

```powershell
go test ./egress/reverse
```

Covered:

- QUIC rejects tunnel fallback flags.
- Tunnel controller falls back from `wlan0` to `rmnet1` after failures.
- Tunnel controller probes and recovers back to `wlan0`.
- Empty fallback interface means system routing.

## Deployment

Deployed to Android only.

- Android binary SHA256:
  `0b28dda562afb69b5f8634fda8316a81335df7e1f4a84259c27943226aa2e67d`
- Android binary backup:
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-tunnel-fallback`
- Android config backup:
  `/data/adb/zhreverse/client.yaml.bak-20260614-tunnel-fallback`
- New Android PID after restart: `14945`; after fallback injection/restore:
  `15816`.

Live config:

```yaml
tunnel_bind_interface: wlan0
tunnel_fallback_interface: rmnet1
tunnel_fallback_after_failures: 3
tunnel_primary_retry_interval: 1m
target_bind_interface: rmnet1
```

## Validation

Normal state:

- `scripts/check-android-egress-health.ps1` PASS.
- Hub `/debug/session-health` sees two sessions from residential IPv4
  `60.124.42.38:*`.
- Android `ss -ntp` shows reverse tunnel sockets
  `192.168.3.3%wlan0 -> 36.50.84.68:39093`.
- `api6.ipify.org` returns cellular IPv6
  `240b:c010:420:43fc:0:22:6ab2:8701`.
- `api.ipify.org` returns phone-side IPv4 `133.106.32.25`.

Fallback injection:

- Temporarily set `tunnel_bind_interface: zhbadv0` while leaving
  `tunnel_fallback_interface: rmnet1`.
- After reconnect, Android `ss -ntp` showed reverse tunnel sockets
  `10.1.7.154%rmnet1 -> 36.50.84.68:39093`.
- A live target socket still used
  `[240b:c010:420:43fc:0:22:6ab2:8701]%rmnet1 -> target:443`.
- Restored `tunnel_bind_interface: wlan0`, restarted `zhreverse`, and sessions
  returned to `192.168.3.3%wlan0`.
- Final health check PASS.

Note: the injection validates the fallback state machine without turning WiFi
off remotely. A physical WiFi-off drill can still be run later, but the safer
failure injection already proves the code path and preserves remote control.
