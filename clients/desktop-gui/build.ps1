#!/usr/bin/env pwsh
# 一键构建大象 VPN 桌面客户端：先产出 dxvpn sidecar，再 tauri build（前端 + Rust + 安装包）。
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $root "..\..")

# 1. Rust host target triple —— sidecar 必须按它命名，tauri externalBin 才能解析。
$triple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
if (-not $triple) { throw "无法获取 rustc host triple（确认已装 Rust）" }
Write-Host "==> target triple: $triple"

# 2. 构建 dxvpn sidecar（内嵌 sing-box，需 with_gvisor）。
$binDir = Join-Path $root "src-tauri\binaries"
New-Item -ItemType Directory -Force $binDir | Out-Null
$out = Join-Path $binDir "dxvpn-$triple.exe"
Write-Host "==> go build sidecar -> $out"
go build -tags with_gvisor -trimpath -ldflags "-s -w" -o $out (Join-Path $repo "clients\cli")
if ($LASTEXITCODE -ne 0) { throw "go build sidecar 失败" }

# 3. tauri build（npm run build 前端 + cargo build + 打包）。
Push-Location $root
try {
    Write-Host "==> npm install"
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install 失败" }
    Write-Host "==> npm run tauri build"
    npm run tauri build
    if ($LASTEXITCODE -ne 0) { throw "tauri build 失败" }
}
finally {
    Pop-Location
}

Write-Host "==> 完成。安装包见 src-tauri/target/release/bundle/"
