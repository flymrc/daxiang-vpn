#!/usr/bin/env pwsh
# Build the Daxiang VPN desktop client: dxvpn sidecar, frontend, Rust app, installer.
param(
    [ValidateSet("amd64", "arm64", "host")]
    [string]$Target = "amd64"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $root "..\..")

# 1. Resolve Rust target triple. Tauri externalBin expects this sidecar suffix.
$hostTriple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
if (-not $hostTriple) { throw "could not get rustc host triple; is Rust installed?" }

$triple = switch ($Target) {
    "amd64" { "x86_64-pc-windows-msvc" }
    "arm64" { "aarch64-pc-windows-msvc" }
    "host" { $hostTriple }
}
$goArch = switch ($triple) {
    "x86_64-pc-windows-msvc" { "amd64" }
    "aarch64-pc-windows-msvc" { "arm64" }
    default { throw "unsupported Windows Rust target triple: $triple" }
}
Write-Host "==> host triple: $hostTriple"
Write-Host "==> target triple: $triple ($goArch)"

$installedTargets = rustup target list --installed
if ($installedTargets -notcontains $triple) {
    Write-Host "==> rustup target add $triple"
    rustup target add $triple
    if ($LASTEXITCODE -ne 0) { throw "rustup target add failed for $triple" }
}

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

Build-Sidecar $triple $goArch

# 3. Tauri build: frontend, Rust, bundle.
Push-Location $root
try {
    Write-Host "==> npm install"
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    Write-Host "==> npm run tauri build -- --target $triple"
    npm run tauri build -- --target $triple
    if ($LASTEXITCODE -ne 0) { throw "tauri build failed" }
}
finally {
    Pop-Location
}

Write-Host "==> done. Installer: src-tauri/target/release/bundle/"
