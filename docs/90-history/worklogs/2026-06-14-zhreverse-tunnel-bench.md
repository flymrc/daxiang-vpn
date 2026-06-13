# 2026-06-14 zhreverse tunnel bench

## Context

After deploying `/debug/session-health`, the next ROI step is to isolate the
Android -> Hub reverse tunnel capacity from public target variability. Real
download tests include DNS, TLS, CDN selection, target behavior, and Rakuten
IPv4/F5 failure modes. A synthetic tunnel benchmark tells us whether lower-level
Go work such as striped CONNECT / multi-stream transfer is likely to pay off.

## Changes

- Added Hub `GET /debug/tunnel-bench?bytes=<total>&streams=<n>`.
- The endpoint reuses the existing `10.66.0.1:18081` proxy listener and remains
  protected by `allowed_proxy_cidrs`.
- Hub splits the requested byte count across concurrent reverse streams and
  sends a new Android-side `BENCH <bytes>` command on each stream.
- Android responds with `OK` and writes synthetic bytes back through the
  reverse tunnel; no public target, DNS, TLS, or CDN is involved.
- The JSON report includes aggregate bytes/Mbps and per-stream bytes, command
  RTT, duration, Mbps, and errors.
- Added guardrails: max `100000000` bytes and max `8` streams per request.
- Updated reverse egress docs, implementation notes, diagnostics runbook, and
  polish plan.

## Validation

- `go test ./egress/reverse` passed.
- `go test ./...` passed.

## Deployment

Deployed to both ends of the production reverse tunnel.

Hub:

- Built `linux/amd64` binary.
- SHA256:
  `6d395a4fd2522b8fb56692e066198595c8df99e6430359eae5c18e41976a0f19`.
- Uploaded to `/tmp/zhreverse-linux-amd64-tunnel-bench`.
- Backed up previous Hub binary:
  `/opt/zongheng/zhreverse/zhreverse.bak-20260614-tunnel-bench`.
- Installed to `/opt/zongheng/zhreverse/zhreverse`.
- Restarted `zhreverse-hub.service`; service became active with PID `248149`.

Android:

- Built `linux/arm64` binary.
- SHA256:
  `77f6bc6a7a5da3968a3480653513ff31e753a620bb253635e308abec2c25acb4`.
- Uploaded via Hub and Android control SSH to
  `/data/local/tmp/zhreverse-tunnel-bench`.
- Backed up previous Android binary:
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-tunnel-bench`.
- Installed via `.staged` + `mv`, then `pkill zhreverse` so the existing
  supervisor relaunched the client.
- New Android `zhreverse client` PID after restart: `10271`.

Post-deploy health:

- `scripts/check-android-egress-health.ps1` PASS.
- v6 egress: `240b:c010:420:43fc:0:22:6ab2:8701`.
- v4 egress: `133.106.32.25`.
- Hub `/debug/session-health`: `session_count=2`, both sessions
  `consecutive_failures=0`, active proxy concurrency returned to 0.
- Temporary files removed from Hub `/tmp`, Android `/data/local/tmp`, and local
  `build/`.

Bench results:

```bash
1MB streams=1: 7.84 Mbps smoke test, ok=true
20MB streams=1: 22.74 Mbps, ok=true
20MB streams=2: 46.57 Mbps, ok=true
20MB streams=4: 48.84 Mbps, ok=true
```

Decision rule:

- The rule triggered in favor of exploring striped CONNECT / multi-stream
  transfer: 2 streams were about 2x faster than 1 stream.
- 4 streams only improved slightly over 2 streams, so the practical next design
  should focus on two-way striping across the two existing reverse sessions and
  avoid adding broad extra stream count by default.

Rollback:

```bash
# Hub
cp /opt/zongheng/zhreverse/zhreverse.bak-20260614-tunnel-bench /opt/zongheng/zhreverse/zhreverse
systemctl restart zhreverse-hub.service

# Android, run through Hub control SSH
cp /data/adb/zhreverse/bin/zhreverse.bak-20260614-tunnel-bench /data/adb/zhreverse/bin/zhreverse.staged
chmod 700 /data/adb/zhreverse/bin/zhreverse.staged
mv /data/adb/zhreverse/bin/zhreverse.staged /data/adb/zhreverse/bin/zhreverse
pkill zhreverse
```
