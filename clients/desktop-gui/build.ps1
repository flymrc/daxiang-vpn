#!/usr/bin/env pwsh
# Build the Daxiang VPN desktop client: dxvpn sidecar, frontend, Rust app, installer.
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $root "..\..")

# 1. Rust host target triple. Tauri externalBin expects this sidecar suffix.
$triple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
if (-not $triple) { throw "could not get rustc host triple; is Rust installed?" }
Write-Host "==> target triple: $triple"

# 2. Build dxvpn sidecars.
$binDir = Join-Path $root "src-tauri\binaries"
New-Item -ItemType Directory -Force $binDir | Out-Null

function Build-Sidecar([string]$targetTriple, [string]$goArch) {
    $out = Join-Path $binDir "dxvpn-$targetTriple.exe"
    Write-Host "==> go build sidecar ($goArch) -> $out"
    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    try {
        $env:GOOS = "windows"
        $env:GOARCH = $goArch
        go build -tags with_gvisor -trimpath -ldflags "-s -w" -o $out (Join-Path $repo "clients\cli")
        if ($LASTEXITCODE -ne 0) { throw "go build sidecar failed for $targetTriple" }
    }
    finally {
        if ($null -eq $oldGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoos }
        if ($null -eq $oldGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoarch }
    }
}

$sidecars = @()
if ($triple -eq "aarch64-pc-windows-msvc") {
    $sidecars += @{ Triple = $triple; Arch = "arm64" }
} elseif ($triple -eq "x86_64-pc-windows-msvc") {
    $sidecars += @{ Triple = $triple; Arch = "amd64" }
} else {
    throw "unsupported Windows Rust host triple: $triple"
}

# Tauri v2 currently bundles x64 NSIS on Windows ARM64, so keep the x64 sidecar present too.
if ($sidecars.Triple -notcontains "x86_64-pc-windows-msvc") {
    $sidecars += @{ Triple = "x86_64-pc-windows-msvc"; Arch = "amd64" }
}

foreach ($sidecar in $sidecars) {
    Build-Sidecar $sidecar.Triple $sidecar.Arch
}

# 3. Tauri build: frontend, Rust, bundle.
Push-Location $root
try {
    Write-Host "==> npm install"
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    Write-Host "==> npm run tauri build"
    npm run tauri build
    if ($LASTEXITCODE -ne 0) { throw "tauri build failed" }
}
finally {
    Pop-Location
}

Write-Host "==> done. Installer: src-tauri/target/release/bundle/"
