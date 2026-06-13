# 2026-06-14 zhreverse tail latency metrics

## Context

The striped CONNECT prototype improved large 20MB downloads, but daily usage
does not need large-file acceleration. The next higher-ROI target is small
request tail latency, heat control, and faster visibility into half-dead
sessions.

## Changes

- Extended Hub `/debug/session-health` with proxy concurrency peaks:
  - `active_proxy_connections_peak`
  - `active_proxy_connections_peak_by_peer`
- Added a rolling in-memory `proxy_metrics` window for recent CONNECT requests:
  - setup latency
  - Android target dial latency
  - first target byte latency
  - total CONNECT duration
  - upload/download byte counts
  - success/failure counters
  - recent failures
- Extended Android CONNECT success status from plain `OK` to:

```text
OK target_dial_ms=<n>
```

Hub still accepts plain `OK`, so mixed-version rollback remains compatible.

- Added `scripts/measure-android-tail-latency.ps1` for small HTTPS request
  p50/p95/p99 testing from the Hub side. The script reports curl
  `appconnect`, `starttransfer`, and `total` timings, then reads
  `/debug/session-health` for Hub-side metrics.

## Validation

- `go test ./egress/reverse -run 'ProxyMetrics|PipeBothMeasured|ParseReverseOKStatus|SessionHealth|Striped|TunnelBench|OpenCommand' -count=1`
  passed.
- `go test ./egress/reverse` passed.
- `go test ./...` passed.
- PowerShell parser check for `scripts/measure-android-tail-latency.ps1`
  passed.

## Deployment

Deployed to production on 2026-06-14. Hub was deployed before Android because
new Hub accepts old plain `OK`, while old Hub would not accept Android's new
`OK target_dial_ms=<n>` status.

- Hub binary SHA256:
  `55fc6e8f91ef14b7c9a6490fcfa48f4194e68138f66b2ff2e8ab9bc5fa231267`
- Hub backup:
  `/opt/zongheng/zhreverse/zhreverse.bak-20260614-tail-latency`
- Android binary SHA256:
  `0cbb1e5ce0a68b07d37ea420ec44e023ac9055cd4ded94b5eff98b641a746fa5`
- Android backup:
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-tail-latency`
- Hub service after deploy: `zhreverse-hub.service` active, PID `253318`.
- Android client after deploy: `zhreverse client`, PID `18902`.

Post-deploy health:

- `scripts/check-android-egress-health.ps1` passed.
- `session_count=2`.
- Both sessions had `consecutive_failures=0`.
- v6 egress remained Rakuten `240b:...`; v4 egress remained phone CGNAT.
- Hub route MTU, TCPMSS rule, and WireGuard handshake were healthy.

Tail latency baseline, `scripts/measure-android-tail-latency.ps1 -Runs 30`:

```text
api64.ipify.org:                 30/30 ok, total_ms p50=756.88 p95=1083.24 p99=1321.40
speed.cloudflare.com 1KB:        30/30 ok, total_ms p50=368.09 p95=779.56  p99=854.80
www.cloudflare.com/cdn-cgi/trace 30/30 ok, total_ms p50=518.88 p95=1132.87 p99=1168.26
Hub proxy_metrics first_byte_ms:           p50=422    p95=870     p99=1180
Hub active_proxy_connections_peak=2
```

`proxy_metrics.failures=8` immediately after deployment; all recent failures
were `no usable reverse client session` during the Hub restart / Android
reconnect window. A follow-up health check passed in steady state.

Rollback:

```powershell
# Hub: restore /opt/zongheng/zhreverse/zhreverse.bak-20260614-tail-latency
# to /opt/zongheng/zhreverse/zhreverse, then restart zhreverse-hub.service.

# Android: restore
# /data/adb/zhreverse/bin/zhreverse.bak-20260614-tail-latency
# to /data/adb/zhreverse/bin/zhreverse, chmod 700, then pkill zhreverse.
```

Decision rule:

- If `active_proxy_connections_peak_by_peer` is high during normal browsing,
  tune per-client concurrency or client behavior before adding heavier
  transport tricks.
- If `target_dial_latency_ms` dominates, focus on DNS/dial path.
- If `first_byte_latency_ms` dominates while setup is normal, focus on target
  TLS/first-flight retry behavior and mobile-network blackholes.
