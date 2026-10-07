#!/usr/bin/env pwsh
# Friendly wrapper for building Zongheng VPN desktop GUI installers.
#
# Examples:
#   .\scripts\build-desktop-gui.ps1 -Target x64
#   .\scripts\build-desktop-gui.ps1 -Target arm64
#   .\scripts\build-desktop-gui.ps1 -Target both -OpenFolder

param(
    # x64/x86 both mean Windows Intel/AMD 64-bit. True 32-bit Windows builds
    # are not supported by the Tauri/Go packaging path in this repo.
    [ValidateSet("x64", "x86", "amd64", "arm64", "host", "both")]
    [string]$Target = "x64",

    [ValidateSet("auto", "cargo", "cargo-xwin")]
    [string]$Runner = "auto",

    [switch]$OpenFolder,
    [switch]$Development,
    [string]$SigningCertificateThumbprint,
    [string]$SigningToolPath,
    [string]$TimestampUrl = "https://timestamp.digicert.com"
)

$ErrorActionPreference = "Stop"

$repo = Resolve-Path (Join-Path $PSScriptRoot "..")
$guiDir = Join-Path $repo "clients\desktop-gui"
$innerBuild = Join-Path $guiDir "build.ps1"

function Resolve-DesktopTarget([string]$value) {
    switch ($value) {
        "x64" { return @("amd64") }
        "x86" { return @("amd64") }
        "amd64" { return @("amd64") }
        "arm64" { return @("arm64") }
        "host" { return @("host") }
        "both" { return @("amd64", "arm64") }
        default { throw "unsupported target: $value" }
    }
}

function Target-Triple([string]$value) {
    switch ($value) {
        "amd64" { return "x86_64-pc-windows-msvc" }
        "arm64" { return "aarch64-pc-windows-msvc" }
        "host" {
            $hostTriple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
            if (-not $hostTriple) { throw "could not get rustc host triple; is Rust installed?" }
            return $hostTriple
        }
        default { throw "unsupported build target: $value" }
    }
}

function Get-InstallerInfo([string]$target) {
    $triple = Target-Triple $target
    $targetBase = Join-Path $guiDir 'src-tauri/target'
    if ($env:CARGO_TARGET_DIR) { $targetBase = [IO.Path]::GetFullPath($env:CARGO_TARGET_DIR) }
    $bundleDir = Join-Path $targetBase "$triple/release/bundle/nsis"
    $manifestPath = Join-Path $bundleDir 'build-manifest.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw 'Current build manifest is missing; refuse to select an older installer.' }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $expectedCommit = (& git -C $repo rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $manifest.source_commit -ne $expectedCommit -or $manifest.target -ne $triple -or $manifest.development -ne [bool]$Development -or @($manifest.artifacts).Count -eq 0) { throw 'Installer manifest identity mismatch.' }
    foreach ($entry in $manifest.artifacts) {
        if ([IO.Path]::GetFileName($entry.file) -ne $entry.file) { throw 'Installer manifest path is invalid.' }
        $installer = Get-Item -LiteralPath (Join-Path $bundleDir $entry.file)
        $hash = Get-FileHash -LiteralPath $installer.FullName -Algorithm SHA256
        if ($hash.Hash.ToLowerInvariant() -ne $entry.sha256) { throw 'Installer hash mismatch.' }
        [pscustomobject]@{
            Target = $target
            Path = $installer.FullName
            SizeMB = [math]::Round($installer.Length / 1MB, 2)
            SHA256 = $hash.Hash
        }
    }
}

$targets = Resolve-DesktopTarget $Target
$results = @()

foreach ($target in $targets) {
    Write-Host "==> building desktop GUI target: $target"
    $buildParameters = @{Target=$target;Runner=$Runner;Development=$Development;TimestampUrl=$TimestampUrl}
    if ($SigningCertificateThumbprint) { $buildParameters.SigningCertificateThumbprint=$SigningCertificateThumbprint }
    if ($SigningToolPath) { $buildParameters.SigningToolPath=$SigningToolPath }
    & $innerBuild @buildParameters
    if ($LASTEXITCODE -ne 0) { throw "desktop GUI build failed for $target" }
    $results += Get-InstallerInfo $target
}

Write-Host ""
Write-Host "==> build outputs"
foreach ($result in $results) {
    Write-Host "Target : $($result.Target)"
    Write-Host "Path   : $($result.Path)"
    Write-Host "SizeMB : $($result.SizeMB)"
    Write-Host "SHA256 : $($result.SHA256)"
    Write-Host ""
}

if ($OpenFolder -and $results.Count -gt 0) {
    Start-Process explorer.exe -ArgumentList "/select,`"$($results[0].Path)`""
}
