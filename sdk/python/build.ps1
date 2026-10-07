param(
    [ValidateSet("x64", "amd64", "arm64", "host")]
    [string]$Target = "host",
    [switch]$Install,
    [switch]$WithRequests,
    [switch]$Wheel,
    [string]$Version = "",
    [switch]$Development,
    [string]$SigningCertificateThumbprint,
    [string]$SigningToolPath,
    [string]$TimestampUrl = "https://timestamp.digicert.com"
)

$ErrorActionPreference = "Stop"
$taskGoEnvironment = @{}
foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) {
    $taskGoEnvironment[$key] = [Environment]::GetEnvironmentVariable($key,'Process')
    [Environment]::SetEnvironmentVariable($key,$null,'Process')
}
try {

$sdkDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $sdkDir "..\..")
$sourceCommit = (& git -C $repo rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $sourceCommit -notmatch '^[a-f0-9]{40}$') { throw 'Exact SDK source commit unavailable.' }
$taskStatus = @(& git -C $repo status --porcelain)
if ($LASTEXITCODE -ne 0) { throw 'SDK source state unavailable.' }
$sourceState = if ($taskStatus.Count) { 'dirty' } else { 'clean' }
. (Join-Path $repo 'scripts/source-manifest.ps1')
$sourceManifest = @(Get-ZhSourceManifest $repo)
if (-not $Development) {
    if ($sourceState -ne 'clean') { throw 'SDK release requires clean source; use -Development for explicit local development.' }
    if (-not $IsWindows -or $SigningCertificateThumbprint -notmatch '^[a-fA-F0-9]{40}$') { throw 'SDK release requires a controlled Windows signing identity.' }
    if (-not $SigningToolPath) { $SigningToolPath = (Get-Command signtool -ErrorAction Stop).Source }
    if (-not (Test-Path -LiteralPath $SigningToolPath -PathType Leaf) -or $TimestampUrl -notmatch '^https://') { throw 'SDK release signing tools unavailable.' }
}
if ($Wheel) { throw 'Bundled native wheel platform/signature acceptance is not implemented; refuse a misleading py3-none-any package.' }
$pyproject = Get-Content (Join-Path $sdkDir "pyproject.toml") -Raw
$packageVersionMatch = [regex]::Match($pyproject, '(?m)^version\s*=\s*"([^"]+)"\s*$')
if (-not $packageVersionMatch.Success) { throw "could not read project version from pyproject.toml" }
$packageVersion = $packageVersionMatch.Groups[1].Value
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = $packageVersion }
if ($Version -ne $packageVersion -or $Version -eq "dev") {
    throw "SDK version mismatch: requested=$Version pyproject=$packageVersion"
}
$binDir = Join-Path $sdkDir "src\zongheng_vpn\bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

function Resolve-Target {
    param([string]$Name)
    if ($Name -eq "host") {
        $arch = (go env GOHOSTARCH).Trim()
        if ($LASTEXITCODE -ne 0) { throw 'SDK host architecture unavailable.' }
        if ($arch -eq "amd64") { return "amd64" }
        if ($arch -eq "arm64") { return "arm64" }
        throw "Unsupported host GOARCH for Windows CLI bundle: $arch"
    }
    if ($Name -eq "x64") { return "amd64" }
    return $Name
}

$goarch = Resolve-Target $Target
$out = Join-Path $binDir "zhvpn.exe"
foreach ($name in @('zhvpn.exe','build-manifest.json','source-manifest.json')) {
    if (Test-Path -LiteralPath (Join-Path $binDir $name)) { throw 'Archive the previous bundled CLI/receipt explicitly; this build never reuses existing artifacts.' }
}
$hostGoOS = (go env GOHOSTOS).Trim()
if ($LASTEXITCODE -ne 0) { throw 'SDK host platform unavailable.' }
$hostGoArch = (go env GOHOSTARCH).Trim()
if ($LASTEXITCODE -ne 0) { throw 'SDK host architecture unavailable.' }
$taskGoVersion = (& go version).Trim()
if ($LASTEXITCODE -ne 0 -or -not $taskGoVersion) { throw 'SDK Go toolchain unavailable.' }

$evidenceDirectory = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-sdk-gates-'+[guid]::NewGuid().ToString('N'))
& (Join-Path $repo 'scripts/check-steelman.ps1') -EvidenceDirectory $evidenceDirectory
if ($LASTEXITCODE -ne 0) { throw 'SDK local gate failed.' }
$previous = @{}
foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
Push-Location $repo
try {
    $env:GOOS = "windows"
    $env:GOARCH = $goarch
    $env:CGO_ENABLED = '0'
    if ($goarch -eq "amd64") {
        $env:GOAMD64 = "v1"
    }
    $ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=python-sdk -X zongheng-vpn/clients/cli/internal/buildinfo.Version=$Version -X zongheng-vpn/clients/cli/internal/buildinfo.SourceCommit=$sourceCommit -X zongheng-vpn/clients/cli/internal/buildinfo.SourceState=$sourceState"
    go build -tags with_gvisor -trimpath -buildvcs=false -ldflags $ldflags -o $out .\clients\cli
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE. If $out is locked, disconnect the SDK-started VPN engine and close Python processes using zongheng_vpn, then retry."
    }
} finally {
    foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    Pop-Location
}

if (-not $Development) {
    & $SigningToolPath sign /sha1 $SigningCertificateThumbprint /fd SHA256 /tr $TimestampUrl /td SHA256 $out
    if ($LASTEXITCODE -ne 0) { throw 'SDK sidecar signing failed.' }
    $signature = Get-AuthenticodeSignature -LiteralPath $out
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $SigningCertificateThumbprint) { throw 'SDK sidecar signature validation failed.' }
}
if ($hostGoOS -eq "windows" -and $goarch -eq $hostGoArch) {
    $taskIdentityJSON = & $out version --json
    if ($LASTEXITCODE -ne 0) { throw 'Bundled CLI identity command failed.' }
    $identity = $taskIdentityJSON | ConvertFrom-Json
    if ($identity.product -ne "python-sdk" -or $identity.version -ne $Version -or $identity.protocol_version -ne 2 -or $identity.source_commit -ne $sourceCommit -or $identity.source_state -ne $sourceState) {
        throw "bundled CLI identity mismatch: $($identity | ConvertTo-Json -Compress)"
    }
    Write-Host ($identity | ConvertTo-Json -Compress)
}

if ($Install) {
    $installTarget = $sdkDir
    if ($WithRequests) {
        $installTarget = "$sdkDir[requests]"
    }
    python -m pip install -e $installTarget
    if ($LASTEXITCODE -ne 0) { throw 'SDK installation failed; no success manifest written.' }
}

if ((& git -C $repo rev-parse HEAD).Trim() -ne $sourceCommit -or $LASTEXITCODE -ne 0) { throw 'SDK source commit changed during build.' }
$taskFinalStatus = @(& git -C $repo status --porcelain)
if ($LASTEXITCODE -ne 0 -or (-not $Development -and $taskFinalStatus.Count)) { throw 'SDK source state changed during build.' }
Assert-ZhSourceManifest $repo $sourceManifest
$sourceManifest | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $binDir 'source-manifest.json') -Encoding utf8
[ordered]@{schema_version=1;product='python-sdk';version=$Version;source_commit=$sourceCommit;source_state=$sourceState;development=[bool]$Development;signed=-not $Development;go_version=$taskGoVersion;cli_contract_version=1;control_protocol_version=1;sidecar_protocol_version=2;target="windows-$goarch";sha256=(Get-FileHash -LiteralPath $out -Algorithm SHA256).Hash.ToLowerInvariant();evidence_directory=$evidenceDirectory} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $binDir 'build-manifest.json') -Encoding utf8
Write-Host "Bundled CLI: $out"
} finally {
    foreach ($key in $taskGoEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$taskGoEnvironment[$key],'Process') }
}
