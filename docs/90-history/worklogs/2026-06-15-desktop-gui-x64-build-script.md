# 2026-06-15 desktop GUI x64 build script

## Context

Need a Windows Intel/AMD build of desktop GUI `0.4.9` and a simple script for
manual builds.

## Changes

- Added `scripts/build-desktop-gui.ps1`, a friendly wrapper around
  `clients/desktop-gui/build.ps1`.
- Supported target aliases:
  - `x64`, `x86`, `amd64` -> Windows x64 / amd64
  - `arm64`
  - `host`
  - `both`
- The wrapper prints installer path, size, and SHA256.
- Updated desktop GUI docs with the new command.

## Validation

```powershell
.\scripts\build-desktop-gui.ps1 -Target x64
```

Build passed and produced:

- Installer:
  `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.9_x64-setup.exe`
- Installer SHA256:
  `844F89C6E0C48E73D392DF7B45EFE47A144323A108A1DF0A97115095F7347F8F`
- Main exe SHA256:
  `C21F26933A13ECD4916872E3D1833D9A41050195B208A7DECE042E5A46BE650A`
- Sidecar SHA256:
  `01A85BAB07338F9787E578DCE88C5E50B7C9679B945F5406C4D98FD7B425BB4F`

PE machine check:

- Installer NSIS stub: `0x014C` (expected for NSIS installer shell).
- `zhvpn-desktop.exe`: `0x8664` (x64/amd64).
- Bundled sidecar source `zhvpn-x86_64-pc-windows-msvc.exe`: `0x8664`
  (x64/amd64).
