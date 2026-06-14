# 2026-06-14 desktop GUI bootstrap throttle

## Context

After testing the Windows ARM GUI, Hub `zhhub.service` logs showed repeated
`/api/client/bootstrap` calls from GUI clients every few seconds. The data path
was healthy, but GUI status polling was turning authorization bootstrap into a
heartbeat.

## Changes

- Changed `zhvpn login` to persist a local status cache with client/egress/proxy
  metadata instead of token-only config.
- Changed `zhvpn status` to read that local cache and avoid Hub bootstrap during
  high-frequency GUI polling.
- Kept `zhvpn start` on a fresh bootstrap path so it still receives the latest
  WireGuard and egress runtime config before launching the proxy.
- Added automatic one-time migration for older token-only configs.
- Kept WireGuard private key in memory for `login`/`start`, but removed it from
  the persisted status cache.
- Bumped desktop GUI package version to `0.4.9` for the Windows ARM rebuild.

## Validation

```powershell
go test ./clients/cli/internal/app
go test ./clients/cli/...
go test ./...
powershell -ExecutionPolicy Bypass -File clients/desktop-gui/build.ps1 -Target arm64
```

All passed.

Windows ARM64 build output:

- Installer:
  `clients/desktop-gui/src-tauri/target/aarch64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.9_arm64-setup.exe`
- Installer SHA256:
  `FC2279F0FF0639F44823B0BCB7AA6034C69FABA05028CCD9DD40ECCB1ACA8D1F`
- Main exe SHA256:
  `DF2571ABC3454CBFE6A7EF3F12F39E026D86C1AD559032234F4AA0FBC7095F75`
- Sidecar SHA256:
  `6BCDDD2B10F4688086E8EFE9098947ACA09EE657C4C621F58C45EE33CDC9242D`

## Expected Effect

GUI main-window and tray status polling should no longer produce repeated
`bootstrap 通过` lines on the Hub. A bootstrap should still appear on login,
first migration from an old token-only config, and connect/start.
