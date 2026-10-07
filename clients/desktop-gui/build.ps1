#!/usr/bin/env pwsh
# Build the Zongheng VPN desktop client: zhvpn sidecar, frontend, Rust app, installer.
param(
    [ValidateSet("amd64", "arm64", "host")]
    [string]$Target = "amd64",

    # auto: use cargo-xwin when building Windows targets from non-Windows hosts.
    [ValidateSet("auto", "cargo", "cargo-xwin")]
    [string]$Runner = "auto",

    [switch]$Development,
    [string]$SigningCertificateThumbprint,
    [string]$SigningToolPath,
    [string]$TimestampUrl = "https://timestamp.digicert.com"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $root "..\..")
$sourceCommit = (& git -C $repo rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $sourceCommit -notmatch '^[0-9a-f]{40}$') { throw 'could not verify the repository full commit SHA' }
$sourceStatus = @(& git -C $repo status --porcelain --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw 'could not verify source working-tree status' }
$sourceState = if ($sourceStatus.Count -eq 0) { 'clean' } else { 'dirty' }
. (Join-Path $repo 'scripts/source-manifest.ps1')
$sourceManifest = @(Get-ZhSourceManifest $repo)
if (-not $Development) {
    if ($sourceState -ne 'clean') { throw 'Release requires a clean source tree. Use -Development for an explicitly marked local development package.' }
    if (-not $IsWindows) { throw 'Release signing must run on the controlled Windows signing host.' }
    if ($SigningCertificateThumbprint -notmatch '^[0-9a-fA-F]{40}$') { throw 'Release requires -SigningCertificateThumbprint for a code-signing certificate.' }
    $certificate = Get-Item -LiteralPath "Cert:/CurrentUser/My/$SigningCertificateThumbprint" -ErrorAction Stop
    $now = Get-Date
    $codeSigning = @($certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' } | ForEach-Object { $_.EnhancedKeyUsages } | Where-Object { $_.Value -eq '1.3.6.1.5.5.7.3.3' })
    if (-not $certificate.HasPrivateKey -or $certificate.NotBefore -gt $now -or $certificate.NotAfter -le $now -or $codeSigning.Count -eq 0) { throw 'Release code-signing certificate is missing a usable key, code-signing purpose, or current validity.' }
    if ([string]::IsNullOrWhiteSpace($SigningToolPath)) {
        $signCommand = Get-Command signtool -ErrorAction SilentlyContinue
        if ($null -eq $signCommand) { throw 'Release requires SignTool on the controlled Windows signing host.' }
        $SigningToolPath = $signCommand.Source
    }
    if (-not (Test-Path -LiteralPath $SigningToolPath -PathType Leaf)) { throw 'Release SignTool path is unavailable.' }
    if ($TimestampUrl -notmatch '^https://') { throw 'Release timestamp URL must use HTTPS.' }
}
else { Write-Host "==> DEVELOPMENT build: $sourceCommit ($sourceState); this is not a signed release." }
$desktopVersion = (Get-Content (Join-Path $root "package.json") -Raw | ConvertFrom-Json).version
$cargoVersion = ((Get-Content (Join-Path $root "src-tauri\Cargo.toml") | Select-String '^version\s*=\s*"([^"]+)"$').Matches.Groups[1].Value)
$tauriVersion = (Get-Content (Join-Path $root "src-tauri\tauri.conf.json") -Raw | ConvertFrom-Json).version
if ([string]::IsNullOrWhiteSpace($desktopVersion) -or $desktopVersion -eq "dev") {
    throw "desktop release version must not be dev"
}
if ($desktopVersion -ne $cargoVersion -or $desktopVersion -ne $tauriVersion) {
    throw "desktop version mismatch: package=$desktopVersion cargo=$cargoVersion tauri=$tauriVersion"
}

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
$targetBase = Join-Path $root 'src-tauri/target'
if (-not [string]::IsNullOrWhiteSpace($env:CARGO_TARGET_DIR)) {
    if (-not [IO.Path]::IsPathRooted($env:CARGO_TARGET_DIR)) { throw 'Build CARGO_TARGET_DIR must be an absolute path.' }
    $targetBase = [IO.Path]::GetFullPath($env:CARGO_TARGET_DIR)
}
$bundleDir = Join-Path $targetBase "$triple/release/bundle/nsis"
if (Test-Path -LiteralPath $bundleDir) {
    if (@(Get-ChildItem -LiteralPath $bundleDir -Force).Count -ne 0) { throw 'Installer output must be empty. Archive prior outputs or choose a fresh absolute CARGO_TARGET_DIR.' }
}
$hostGoOS = (go env GOOS).Trim()
$hostGoArch = (go env GOARCH).Trim()
Write-Host "==> host triple: $hostTriple"
Write-Host "==> target triple: $triple ($goArch)"

$installedTargets = rustup target list --installed
if ($installedTargets -notcontains $triple) {
    Write-Host "==> rustup target add $triple"
    rustup target add $triple
    if ($LASTEXITCODE -ne 0) { throw "rustup target add failed for $triple" }
}

# 2. Lock dependencies and verify the implemented safety gates before creating
# sidecars/installers. Any failing command throws and ends this build.
Push-Location $root
try {
    Write-Host "==> npm ci"
    npm ci
    if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
    $tauriCliVersion = (Get-Content -LiteralPath (Join-Path $root 'node_modules/@tauri-apps/cli/package.json') -Raw | ConvertFrom-Json).version
    $lockedTauriCliVersion = (Get-Content -LiteralPath (Join-Path $root 'package-lock.json') -Raw | ConvertFrom-Json -AsHashtable)['packages']['node_modules/@tauri-apps/cli']['version']
    if ($tauriCliVersion -ne '2.11.2' -or $lockedTauriCliVersion -ne '2.11.2') { throw 'The fresh-only installer template requires verified Tauri CLI 2.11.2; review template compatibility before changing the CLI.' }
}
finally { Pop-Location }
$gateEvidence = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-build-gates-' + [guid]::NewGuid().ToString('N'))
& (Join-Path $repo "scripts/check-steelman.ps1") -EvidenceDirectory $gateEvidence
if ($LASTEXITCODE -ne 0) { throw "Steelman behavior and security gate failed" }

# 3. Build zhvpn sidecars.
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
        $ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=desktop-gui -X zongheng-vpn/clients/cli/internal/buildinfo.Version=$desktopVersion -X zongheng-vpn/clients/cli/internal/buildinfo.SourceCommit=$sourceCommit -X zongheng-vpn/clients/cli/internal/buildinfo.SourceState=$sourceState"
        go build -buildvcs=false -tags with_gvisor -trimpath -ldflags $ldflags -o $out (Join-Path $repo "clients\cli")
        if ($LASTEXITCODE -ne 0) { throw "go build sidecar failed for $targetTriple" }
        if ($hostGoOS -eq "windows" -and $goArch -eq $hostGoArch) {
            $identity = (& $out version --json | ConvertFrom-Json)
            if ($identity.product -ne "desktop-gui" -or $identity.version -ne $desktopVersion -or $identity.protocol_version -ne 2 -or $identity.source_commit -ne $sourceCommit -or $identity.source_state -ne $sourceState) {
                throw "sidecar identity mismatch: $($identity | ConvertTo-Json -Compress)"
            }
        }
        if (-not $Development) {
            & $SigningToolPath sign /sha1 $SigningCertificateThumbprint /fd SHA256 /tr $TimestampUrl /td SHA256 $out
            if ($LASTEXITCODE -ne 0) { throw "sidecar signing failed: $targetTriple" }
            $signature = Get-AuthenticodeSignature -LiteralPath $out
            if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $SigningCertificateThumbprint) { throw 'Sidecar signature did not validate against the selected release certificate.' }
        }
    }
    finally {
        if ($null -eq $oldGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoos }
        if ($null -eq $oldGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoarch }
        if ($null -eq $oldGoamd64) { Remove-Item Env:GOAMD64 -ErrorAction SilentlyContinue } else { $env:GOAMD64 = $oldGoamd64 }
    }
}

Build-Sidecar $triple $goArch

# 4. Tauri build: frontend, Rust, bundle.
Push-Location $root
$hadTauriConfig = Test-Path Env:TAURI_CONFIG
$previousTauriConfig = $env:TAURI_CONFIG
try {
    if (-not $Development) {
        $releaseConfig = if ($hadTauriConfig) { $previousTauriConfig | ConvertFrom-Json -AsHashtable } else { @{} }
        if (-not $releaseConfig.ContainsKey('bundle')) { $releaseConfig.bundle = @{} }
        if (-not $releaseConfig.bundle.ContainsKey('windows')) { $releaseConfig.bundle.windows = @{} }
        $releaseConfig.bundle.externalBin = @('binaries/zhvpn')
        $releaseConfig.bundle.windows.certificateThumbprint = $SigningCertificateThumbprint
        $releaseConfig.bundle.windows.digestAlgorithm = 'sha256'
        $releaseConfig.bundle.windows.timestampUrl = $TimestampUrl
        if (-not $releaseConfig.bundle.windows.ContainsKey('nsis')) { $releaseConfig.bundle.windows.nsis = @{} }
        $releaseConfig.bundle.windows.nsis.installMode = 'currentUser'
        $releaseConfig.bundle.windows.nsis.template = 'installer-fresh-only.nsi'
        $releaseConfig.bundle.windows.nsis.installerHooks = 'installer-hooks.nsh'
        $env:TAURI_CONFIG = $releaseConfig | ConvertTo-Json -Depth 20 -Compress
    }
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
    if ($hadTauriConfig) { $env:TAURI_CONFIG = $previousTauriConfig }
    else { Remove-Item Env:TAURI_CONFIG -ErrorAction SilentlyContinue }
    Pop-Location
}

$finalCommit = (& git -C $repo rev-parse HEAD).Trim()
$finalStatus = @(& git -C $repo status --porcelain --untracked-files=all)
if ($LASTEXITCODE -ne 0 -or $finalCommit -ne $sourceCommit -or ($sourceState -eq 'clean' -and $finalStatus.Count -ne 0)) { throw 'Source changed during the build; the output is not an approved release.' }
$installers = @(Get-ChildItem -LiteralPath $bundleDir -Filter '*.exe' -File)
if ($installers.Count -eq 0) { throw 'No installer was produced.' }
$entries = foreach ($installer in $installers) {
    if ($Development) {
        $devName = "$($installer.BaseName).dev-$($sourceCommit.Substring(0,12))-unsigned.exe"
        $devPath = Join-Path $bundleDir $devName
        if ($installer.FullName -ne $devPath) { Move-Item -LiteralPath $installer.FullName -Destination $devPath }
        $artifact = Get-Item -LiteralPath $devPath
    }
    else {
        $artifact = $installer
        $signature = Get-AuthenticodeSignature -LiteralPath $artifact.FullName
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $SigningCertificateThumbprint) { throw 'Installer signature did not validate against the selected release certificate.' }
    }
    @{ file = $artifact.Name; sha256 = (Get-FileHash -LiteralPath $artifact.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
}
Assert-ZhSourceManifest $repo $sourceManifest
$sourceManifestPath = Join-Path $bundleDir 'source-manifest.json'
$sourceManifest | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $sourceManifestPath -Encoding utf8
$sourceManifestHash = (Get-FileHash -LiteralPath $sourceManifestPath -Algorithm SHA256).Hash.ToLowerInvariant()
@{ product = 'desktop-gui'; version = $desktopVersion; source_commit = $sourceCommit; source_state = $sourceState; source_manifest_file = 'source-manifest.json'; source_manifest_sha256 = $sourceManifestHash; development = [bool]$Development; target = $triple; signed = -not $Development; artifacts = @($entries) } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $bundleDir 'build-manifest.json') -Encoding utf8
Write-Host "==> done. Installer: $bundleDir"
