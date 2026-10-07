# Both legacy CLI build entries delegate here. No publishing or installation.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][ValidateSet('windows','macos')][string]$Platform,
    [Parameter(Mandatory=$true)][string]$OutputDirectory,
    [string]$Version = 'dev',
    [switch]$Development
)
$ErrorActionPreference = 'Stop'
if (-not $Development) {
    throw 'CLI release signing/notarization and verification are not implemented or accepted. Release refused; use -Development only for explicit unsigned development artifacts.'
}
if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$') { throw 'Invalid CLI development version.' }
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'source-manifest.ps1')
$taskOutput = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $taskOutput) { throw 'Use a fresh output directory; existing artifacts are never reused, replaced or deleted.' }
$previous = @{}
foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
Push-Location $repoRoot
try {
    # Gates run for the actual host, independent of inherited cross-build env.
    foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$null,'Process') }
    $taskCommit = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $taskCommit -notmatch '^[a-f0-9]{40}$') { throw 'Exact source commit unavailable.' }
    $status = @(& git status --porcelain)
    if ($LASTEXITCODE -ne 0) { throw 'Source state unavailable.' }
    $taskState = if ($status.Count) { 'dirty' } else { 'clean' }
    $sourceHashes = @(Get-ZhSourceManifest $repoRoot)
    $toolchain = (& go version).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $toolchain) { throw 'Go toolchain unavailable.' }
    $hostOS = (& go env GOHOSTOS).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Go host platform unavailable.' }
    $hostArch = (& go env GOHOSTARCH).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Go host architecture unavailable.' }
    New-Item -ItemType Directory -Path $taskOutput | Out-Null
    $evidenceDirectory = Join-Path $taskOutput 'gate-evidence'
    & (Join-Path $PSScriptRoot 'check-steelman.ps1') -EvidenceDirectory $evidenceDirectory
    if ($LASTEXITCODE -ne 0) { throw 'CLI local gate failed; no artifact manifest written.' }
    Assert-ZhSourceManifest $repoRoot $sourceHashes
    $targetOS = if ($Platform -eq 'macos') { 'darwin' } else { 'windows' }
    $artifacts = @()
    foreach ($arch in @('amd64','arm64')) {
        $env:GOOS=$targetOS; $env:GOARCH=$arch; $env:GOAMD64='v1'; $env:CGO_ENABLED='0'
        $name = "zhvpn-$targetOS-$arch" + $(if ($targetOS -eq 'windows') { '.exe' } else { '' })
        $out = Join-Path $taskOutput $name
        $flags = "-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=cli -X zongheng-vpn/clients/cli/internal/buildinfo.Version=$Version -X zongheng-vpn/clients/cli/internal/buildinfo.SourceCommit=$taskCommit -X zongheng-vpn/clients/cli/internal/buildinfo.SourceState=$taskState"
        & go build -tags with_gvisor -trimpath -buildvcs=false -ldflags $flags -o $out ./clients/cli
        if ($LASTEXITCODE -ne 0) { throw "CLI build failed for $targetOS/$arch; no artifact manifest written." }
        if (-not (Test-Path -LiteralPath $out -PathType Leaf)) { throw 'CLI build output missing.' }
        if ($hostOS -eq $targetOS -and $hostArch -eq $arch) {
            $identity = (& $out version --json | ConvertFrom-Json)
            if ($LASTEXITCODE -ne 0 -or $identity.product -ne 'cli' -or $identity.version -ne $Version -or $identity.protocol_version -ne 2 -or $identity.source_commit -ne $taskCommit -or $identity.source_state -ne $taskState) { throw 'CLI artifact identity mismatch.' }
        }
        $artifacts += [pscustomobject]@{file=$name;os=$targetOS;arch=$arch;sha256=(Get-FileHash -LiteralPath $out -Algorithm SHA256).Hash.ToLowerInvariant();signed=$false;acceptance='compile_only'}
    }
    if ((& git rev-parse HEAD).Trim() -ne $taskCommit -or $LASTEXITCODE -ne 0) { throw 'Source commit changed during build.' }
    $finalStatus = @(& git status --porcelain)
    if ($LASTEXITCODE -ne 0 -or $(if ($finalStatus.Count) { 'dirty' } else { 'clean' }) -ne $taskState) { throw 'Source state changed during build.' }
    Assert-ZhSourceManifest $repoRoot $sourceHashes
    $sourceHashes | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $taskOutput 'source-manifest.json') -Encoding utf8
    [ordered]@{schema_version=1;product='cli';channel='development';version=$Version;source_commit=$taskCommit;source_state=$taskState;development=$true;signed=$false;release_ready=$false;go_version=$toolchain;source_manifest_sha256=(Get-FileHash -LiteralPath (Join-Path $taskOutput 'source-manifest.json') -Algorithm SHA256).Hash.ToLowerInvariant();sidecar_protocol_version=2;evidence_directory=$evidenceDirectory;artifacts=$artifacts} |
        ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $taskOutput 'build-manifest.json') -Encoding utf8
    Write-Host "Unsigned CLI development manifest: $(Join-Path $taskOutput 'build-manifest.json')"
} finally {
    foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    Pop-Location
}
