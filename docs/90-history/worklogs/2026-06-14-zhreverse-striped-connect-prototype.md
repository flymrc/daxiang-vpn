# 2026-06-14 zhreverse striped CONNECT prototype

## Context

The deployed tunnel benchmark showed that Android -> Hub reverse tunnel
throughput nearly doubled when using two reverse streams:

- 20MB, 1 stream: `22.74 Mbps`
- 20MB, 2 streams: `46.57 Mbps`
- 20MB, 4 streams: `48.84 Mbps`

This made a two-lane download prototype worth trying, but only as an opt-in
path so the stable default `CONNECT` behavior remains untouched.

## Changes

- Added per-request opt-in header `X-ZH-Striped-Streams: 2`.
- Default `CONNECT` path remains unchanged.
- Hub opens two reverse streams with:

```text
STRIPED_CONNECT <id> <lane> <lanes> <target>
```

- Android creates one target TCP connection for the group.
- Lane 0 carries client -> target upload bytes.
- Android frames target -> client download bytes across both lanes.
- Android uses one writer goroutine per lane so slow writes on one reverse
  stream do not serialize all outbound frames.
- Hub reorders frames by sequence number and writes ordered bytes to the client.
- The prototype currently supports exactly two lanes.
- Hub caps pending reorder memory at `8MB`.

## Validation

- `go test ./egress/reverse -run 'Striped|TunnelBench|OpenCommand' -count=1`
  passed.
- `go test ./egress/reverse` passed.
- `go test ./...` passed.
- `go test -race ./egress/reverse` could not run in this workspace because
  Go reports `-race is not supported on windows/arm64`.

## Deployment

Deployed to production on 2026-06-14. Both Hub `linux/amd64` and Android
`linux/arm64` binaries were replaced because both sides need the new
`STRIPED_CONNECT` command.

- Hub binary SHA256:
  `0911c09868a1842cbba5aa6367b9fdd47dbf4769b53485bc25a5c1aded2b3f04`
- Hub backup:
  `/opt/zongheng/zhreverse/zhreverse.bak-20260614-striped-connect-writers`
- Android binary SHA256:
  `b7597b74ceae75d89793690716b25fcb6edeb877c6afa9ab921aded195dfa745`
- Android backup:
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-striped-connect-writers`
- Hub service after deploy: `zhreverse-hub.service` active, PID `251516`.
- Android client after deploy: `zhreverse client`, PID `15766`.

The first deployment used a sequential lane writer and showed only partial
gain. It was immediately replaced with the per-lane writer build above.

Post-deploy health:

- `scripts/check-android-egress-health.ps1` passed.
- `session_count=2`.
- Both sessions had `consecutive_failures=0`.
- v6 egress remained Rakuten `240b:...`; v4 egress remained phone CGNAT.
- Hub route MTU, TCPMSS rule, and WireGuard handshake were healthy.

20MB sequential A/B from the Hub:

```text
normal20_seq  code=200 bytes=20000000 Bps=3252101 seconds=6.149869
striped20_seq code=200 bytes=20000000 Bps=4421103 seconds=4.523757
```

The striped path was about 36% faster than the default CONNECT path in this
run. It is useful enough to keep as an explicit large-download experiment, but
not yet strong enough to auto-enable.

Smoke command:

```bash
curl --proxy http://10.66.0.1:18081 \
  --proxy-header 'X-ZH-Striped-Streams: 2' \
  -L -o /dev/null \
  'https://speed.cloudflare.com/__down?bytes=20000000'
```

Rollback:

- Hub: restore
  `/opt/zongheng/zhreverse/zhreverse.bak-20260614-striped-connect-writers`
  to `/opt/zongheng/zhreverse/zhreverse`, then restart
  `zhreverse-hub.service`.
- Android: restore
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-striped-connect-writers`
  to `/data/adb/zhreverse/bin/zhreverse`, `chmod 700`, then `pkill zhreverse`
  and let the service script restart it.

Decision rule:

- If striped real HTTPS downloads approach the `tunnel-bench` two-stream gain,
  evaluate automatic enablement for large downloads only.
- If the gain is weak or unstable, keep the path as an explicit diagnostic
  switch.
