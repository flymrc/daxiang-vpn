#!/usr/bin/env pwsh
# Build the Zongheng VPN desktop client: zhvpn sidecar, frontend, Rust app, installer.
param(
    [ValidateSet("amd64", "arm64", "host")]
    [string]$Target = "amd64",

    # auto: use cargo-xwin when building Windows targets from non-Windows hosts.
    [ValidateSet("auto", "cargo", "cargo-xwin")]
    [string]$Runner = "auto"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $root "..\..")
$desktopVersion = (Get-Content (Join-Path $root "package.json") -Raw | ConvertFrom-Json).version

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

# 2. Build zhvpn sidecars.
$binDir = Join-Path $root "src-tauri\binaries"
New-Item -ItemType Directory -Force $binDir | Out-Null

function Build-Sidecar([string]$targetTriple, [string]$goArch) {
    $out = Join-Path $binDir "zhvpn-$targetTriple.exe"
    Write-Host "==> go build sidecar ($goArch) -> $out"
    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    $oldGoamd64 = $env:GOAMD64
    try {
        $env:GOOS = "windows"
        $env:GOARCH = $goArch
        if ($goArch -eq "amd64") {
            # Maximum compatibility with older Intel i5 / Win10 machines.
            $env:GOAMD64 = "v1"
        }
        $ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/app.Version=$desktopVersion"
        go build -tags with_gvisor -trimpath -ldflags $ldflags -o $out (Join-Path $repo "clients\cli")
        if ($LASTEXITCODE -ne 0) { throw "go build sidecar failed for $targetTriple" }
    }
    finally {
        if ($null -eq $oldGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoos }
        if ($null -eq $oldGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoarch }
        if ($null -eq $oldGoamd64) { Remove-Item Env:GOAMD64 -ErrorAction SilentlyContinue } else { $env:GOAMD64 = $oldGoamd64 }
    }
}

Build-Sidecar $triple $goArch

# 3. Tauri build: frontend, Rust, bundle.
Push-Location $root
try {
    Write-Host "==> npm install"
    npm install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    $buildArgs = @("run", "tauri", "build", "--", "--target", $triple)
    $isWindowsHost = $hostTriple -like "*-pc-windows-msvc"
    if ($Runner -eq "cargo-xwin" -or ($Runner -eq "auto" -and -not $isWindowsHost)) {
        if (-not (Get-Command cargo-xwin -ErrorAction SilentlyContinue)) {
            throw "cargo-xwin is required for cross-building Windows targets from this host. Install with: cargo install --locked cargo-xwin"
        }
        $buildArgs += @("--runner", "cargo-xwin")
    } elseif ($Runner -eq "cargo-xwin") {
        $buildArgs += @("--runner", "cargo-xwin")
    }
    Write-Host "==> npm $($buildArgs -join ' ')"
    & npm @buildArgs
    if ($LASTEXITCODE -ne 0) { throw "tauri build failed" }
}
finally {
    Pop-Location
}

$bundleDir = Join-Path $root "src-tauri\target\$triple\release\bundle\nsis"
if (-not (Test-Path $bundleDir)) { $bundleDir = Join-Path $root "src-tauri\target\release\bundle\nsis" }
Write-Host "==> done. Installer: $bundleDir"
