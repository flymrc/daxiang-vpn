#!/usr/bin/env bash
# Explicit development build entry point. Formal macOS signing/notarization
# remains unavailable; the shared builder refuses release before any build.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec pwsh -NoLogo -NoProfile -NonInteractive -File "$repo_root/scripts/build-cli.ps1" -Platform macos "$@"
