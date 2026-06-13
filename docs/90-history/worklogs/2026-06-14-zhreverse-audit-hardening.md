# 2026-06-14 zhreverse audit hardening

## Context

After reviewing today's Android reverse egress changes, two areas needed
tightening rather than new features:

- striped CONNECT cancellation/error paths should not leave the Hub handler
  waiting forever for more frames.
- `/debug/tunnel-bench` should not be callable by every normal proxy client,
  because it can push synthetic traffic through the phone and cause heat.

## Changes

- `relayStripedConnect` now waits for striped reader goroutines and closes the
  frame channel when all readers have exited.
- `writeOrderedStripedFramesMeasured` returns `io.ErrUnexpectedEOF` when the
  frame channel closes before an ordered EOF frame arrives, instead of waiting
  forever.
- Added `server.debug_allowed_cidrs`.
- Debug endpoints (`/debug/session-health` and `/debug/tunnel-bench`) now
  require both normal proxy access and debug access.
- Default behavior remains backward compatible: when `debug_allowed_cidrs` is
  omitted, it inherits `allowed_proxy_cidrs`.
- The example Hub config explicitly narrows debug access to `10.66.0.1/32`.

## Validation

- `go test ./egress/reverse -run 'DebugAllowed|WriteOrderedStripedFrames|StripedConnect|LoadReverseConfig' -count=1`
  passed.
- `go test ./egress/reverse` passed.
- `go test ./...` passed.

## Deployment

Deployed to the Hub on 2026-06-14. Android did not need a binary replacement
for this hardening.

- Hub binary SHA256:
  `91dbae431dece50d8e034b369cd936a388e1a19a8b716097832773fe23b24ef9`
- Previous Hub binary backup:
  `/opt/zongheng/zhreverse/zhreverse.bak-20260614-audit-hardening`
- Hub config backup:
  `/etc/zongheng/zhreverse/server.yaml.bak-20260614-audit-hardening`

The production Hub config now includes:

```yaml
debug_allowed_cidrs:
  - 10.66.0.1/32
```

Post-deploy checks:

- `zhreverse-hub.service` active.
- Hub-local `/debug/session-health` succeeded.
- Hub-local `/debug/tunnel-bench?bytes=1024&streams=1` succeeded.
- `scripts/check-android-egress-health.ps1` passed.
- Android reverse sessions: 2, both `consecutive_failures=0`.

Rollback:

- Restore the previous Hub binary backup.
- Restore the config backup, or set `debug_allowed_cidrs` back to the broader proxy CIDR if
  diagnostics are accidentally blocked.
