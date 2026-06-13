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

Not deployed yet. This requires replacing both Hub `linux/amd64` and Android
`linux/arm64` binaries because Android now reports `target_dial_ms` and Hub
records it.

Suggested smoke after deployment:

```powershell
.\scripts\check-android-egress-health.ps1
.\scripts\measure-android-tail-latency.ps1 -Runs 30
```

Decision rule:

- If `active_proxy_connections_peak_by_peer` is high during normal browsing,
  tune per-client concurrency or client behavior before adding heavier
  transport tricks.
- If `target_dial_latency_ms` dominates, focus on DNS/dial path.
- If `first_byte_latency_ms` dominates while setup is normal, focus on target
  TLS/first-flight retry behavior and mobile-network blackholes.
