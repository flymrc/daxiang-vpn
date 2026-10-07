# Explicit unsigned development artifacts, with source/toolchain/hash evidence.
# This script never publishes, installs, restarts or accepts a dirty release.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'source-manifest.ps1')
$taskOutput = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $taskOutput) { throw 'Use a new output directory; existing artifacts are never reused or deleted.' }
Push-Location $repoRoot
try {
    $taskCommit = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $taskCommit -notmatch '^[a-f0-9]{40}$') { throw 'Exact source commit unavailable.' }
    $taskState = if (& git status --porcelain) { 'dirty' } else { 'clean' }
    & go run ./shared/contracts/cmd/contractgen -check
    if ($LASTEXITCODE -ne 0) { throw 'Generated contract gate failed.' }
    & go run ./shared/proxygate/cmd/schemagen -check
    if ($LASTEXITCODE -ne 0) { throw 'Proxy admission contract gate failed.' }
    & (Join-Path $PSScriptRoot 'check-device-contract.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Device OpenAPI gate failed.' }
    & (Join-Path $PSScriptRoot 'check-admin-contract.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Admin OpenAPI/sqlc gate failed.' }
    New-Item -ItemType Directory -Path $taskOutput | Out-Null
    $sourceHashes = @(Get-ZhSourceManifest $repoRoot)
    $sourceHashes | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $taskOutput 'source-hashes.json') -Encoding utf8
    $targets = @(
        @{name='zhvpn-windows-amd64.exe';os='windows';arch='amd64';package='./clients/cli'},
        @{name='zhvpn-windows-arm64.exe';os='windows';arch='arm64';package='./clients/cli'},
        @{name='zhvpn-darwin-amd64';os='darwin';arch='amd64';package='./clients/cli'},
        @{name='zhvpn-darwin-arm64';os='darwin';arch='arm64';package='./clients/cli'},
        @{name='zhhub-linux-amd64';os='linux';arch='amd64';package='./hub'},
        @{name='zhhub-device-executor-linux-amd64';os='linux';arch='amd64';package='./hub/cmd/zhhub-device-executor'},
        @{name='zhhub-campaign-register-linux-amd64';os='linux';arch='amd64';package='./hub/admin/cmd/campaign-register'},
        @{name='zhreverse-linux-amd64';os='linux';arch='amd64';package='./egress/reverse'},
        @{name='zhreverse-linux-arm64';os='linux';arch='arm64';package='./egress/reverse'},
        @{name='zhandroid-control-linux-arm64';os='linux';arch='arm64';package='./egress/android-control'}
    )
    $previous = @{}
    foreach ($key in @('GOOS','GOARCH','CGO_ENABLED','GOAMD64')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
    $artifacts = @()
    try {
        foreach ($target in $targets) {
            $env:GOOS=$target.os; $env:GOARCH=$target.arch; $env:CGO_ENABLED='0'; $env:GOAMD64='v1'
            $path=Join-Path $taskOutput $target.name
            $flags="-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=cli -X zongheng-vpn/clients/cli/internal/buildinfo.Version=dev -X zongheng-vpn/clients/cli/internal/buildinfo.SourceCommit=$taskCommit -X zongheng-vpn/clients/cli/internal/buildinfo.SourceState=$taskState"
            & go build -tags with_gvisor -trimpath -buildvcs=false -ldflags $flags -o $path $target.package
            if ($LASTEXITCODE -ne 0) { throw "Build failed for $($target.name); no success manifest written." }
            $artifacts += [pscustomobject]@{name=$target.name;os=$target.os;arch=$target.arch;package=$target.package;sha256=(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant();signed=$false;acceptance='compile_only'}
        }
    } finally {
        foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    }
    if ((& git rev-parse HEAD).Trim() -ne $taskCommit) { throw 'Source commit changed during build.' }
    Assert-ZhSourceManifest $repoRoot $sourceHashes
    [ordered]@{schema_version=1;product='zhvpn';channel='development';version='dev';source_commit=$taskCommit;source_state=$taskState;source_manifest_sha256=(Get-FileHash -LiteralPath (Join-Path $taskOutput 'source-hashes.json') -Algorithm SHA256).Hash.ToLowerInvariant();go_version=(& go version).Trim();cli_contract_version=1;control_protocol_version=1;sidecar_protocol_version=2;release_ready=$false;artifacts=$artifacts} |
        ConvertTo-Json -Depth 7 | Set-Content -LiteralPath (Join-Path $taskOutput 'manifest.json') -Encoding utf8
    Write-Host "Unsigned development build manifest: $(Join-Path $taskOutput 'manifest.json')"
} finally { Pop-Location }
