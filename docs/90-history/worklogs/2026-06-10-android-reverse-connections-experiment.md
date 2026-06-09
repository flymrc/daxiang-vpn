# 2026-06-10 Android reverse connections experiment

## Context

User reported that the Android egress IP was reachable but browsing felt abnormally stuck. We verified that traffic did go through Hub `10.66.0.1:18081` and then the Android reverse tunnel, not the removed legacy `10.66.0.101:1080` path.

## Evidence

- Hub direct public IP: `36.50.84.68`.
- Hub via `http://10.66.0.1:18081`: Android/Rakuten public IP `133.106.154.188`.
- Hub sockets showed client `10.66.0.30` established to `10.66.0.1:18081`.
- Hub sockets showed Android reverse TCP sessions from `133.106.154.188` to Hub `39093/tcp`.
- Android Wi-Fi was disabled and cellular service was Rakuten LTE Band 3, EARFCN 1500.
- Android direct download at the same time was materially faster than reverse proxy download, so the bottleneck was in the reverse data path and/or client concurrency, not simply a dead radio.

## Experiment

Changed Android `/data/adb/dxreverse/client.yaml`:

```yaml
client:
  transport: tcp
  connections: 2
```

Restarted `dxreverse client`. Hub confirmed two established reverse TCP/yamux sessions.

Also tested `connections: 4`; it produced four reverse sessions but did not improve throughput enough to justify the higher half-dead-session risk on mobile NAT.

## Results

Baseline before this change:

- 2 MB reverse proxy sample set: about `0.89-2.53 Mbps`, average `1.90 Mbps`.
- 20 MB reverse proxy test timed out after 25 seconds with partial downloads.
- Android direct 2 MB sample: about `6.6 Mbps` in earlier same-session testing.

`connections: 2`:

- Health check passed.
- 2 MB x5 reverse proxy: average `1.40 Mbps`, min `1.07 Mbps`, max `1.86 Mbps`.
- 10 MB x3 reverse proxy: two successful samples at about `2.33-2.44 Mbps`; one timed out after receiving about `6.8 MB`.

`connections: 4`:

- Health check passed.
- 2 MB x5 reverse proxy: average `1.53 Mbps`, min `1.06 Mbps`, max `2.33 Mbps`.
- 10 MB x3 reverse proxy: average `2.30 Mbps`, min `2.10 Mbps`, max `2.69 Mbps`.

## Decision

Keep production at `connections: 2`.

This improves the topology from a single reverse TCP/yamux bottleneck while avoiding the extra mobile-session risk of `connections: 4`, which did not materially outperform `2` in this run.

## Hub concurrency guard

After the connection-count experiment, added Hub-side CONNECT concurrency protection to `egress/reverse`:

- `server.max_proxy_connections`
- `server.max_proxy_connections_per_client`

Production Hub `/etc/daxiang/dxreverse/server.yaml` now uses:

```yaml
server:
  max_proxy_connections: 96
  max_proxy_connections_per_client: 48
```

When the guard is hit, `dxreverse` returns HTTP 429 instead of allowing unbounded client-side CONNECTs to pile up inside the Android mobile tunnel.

Deployment:

- Built `GOOS=linux GOARCH=amd64` `egress/reverse`.
- Backed up Hub binary and config.
- Replaced `/opt/daxiang/dxreverse/dxreverse`.
- Restarted `dxreverse-hub.service`.
- Hub log confirmed: `max_proxy_connections=96 max_proxy_connections_per_client=48`.

Post-deploy validation:

- `check-android-egress-health.ps1`: PASS.
- Hub showed 2 established reverse TCP sessions from Android public IP `133.106.154.188`.
- 2 MB reverse proxy x3 after deploy: average `2.43 Mbps`, min `1.73 Mbps`, max `3.07 Mbps`.

Follow-up after GUI testing:

- User reported frequent disconnects in the desktop GUI.
- Local repro showed `curl -x http://127.0.0.1:7890 https://api.ipify.org` succeeded a few times, then returned repeated `curl (35) Recv failure: Connection was reset`.
- Hub showed exactly 12 established client connections to `10.66.0.1:18081`, matching the previous per-client cap.
- Raised production limits from `32/12` to `96/48`.
- After the change, 8 consecutive local proxy IP checks succeeded without reset.

## Next

The remaining abnormal stuck feeling likely needs code-level protection in Hub-side `dxreverse`:

- Prefer short interactive requests over bulk/long downloads.
- Consider separate interactive and bulk reverse session pools.
- Add better observability for active proxy count, session selection, command latency, and stream copy duration.
