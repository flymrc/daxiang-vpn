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

Pending before deployment:

```powershell
go test ./egress/reverse
go test ./...
```

## Deployment

Not deployed yet. Deployment should update the Hub binary and add this to
`/etc/zongheng/zhreverse/server.yaml`:

```yaml
debug_allowed_cidrs:
  - 10.66.0.1/32
```

Android binary changes are not required for this hardening, but deploying the
same code to Android is harmless.

Rollback:

- Restore the previous Hub binary backup.
- Remove `debug_allowed_cidrs` or set it back to the broader proxy CIDR if
  diagnostics are accidentally blocked.
