# Exercise the actual builder with isolated Git source and fake compiler/gate.
# This proves refusal/manifest behavior, not executable or platform acceptance.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-cli-builder-tests-'+[guid]::NewGuid().ToString('N'))
$repo = Join-Path $fixture 'source'
$scripts = Join-Path $repo 'scripts'
New-Item -ItemType Directory -Path $scripts | Out-Null
foreach ($name in @('build-cli.ps1','source-manifest.ps1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $scripts $name) }
Set-Content -LiteralPath (Join-Path $repo 'README.md') -Value 'fixture source' -Encoding utf8
@'
param([Parameter(Mandatory=$true)][string]$EvidenceDirectory)
$global:ZhBuildTest.GateCount++
if ($global:ZhBuildTest.Mode -eq 'gate-fails') { throw 'synthetic gate failure' }
New-Item -ItemType Directory -Path $EvidenceDirectory | Out-Null
Set-Content -LiteralPath (Join-Path $EvidenceDirectory 'gate.txt') -Value 'synthetic gate receipt'
if ($global:ZhBuildTest.Mode -eq 'gate-source-change') { Add-Content -LiteralPath (Join-Path $global:ZhBuildTest.Repository 'README.md') -Value 'changed during gate' }
$global:LASTEXITCODE=0
'@ | Set-Content -LiteralPath (Join-Path $scripts 'check-steelman.ps1') -Encoding utf8
$git = @(Get-Command git -CommandType Application)[0].Source
& $git -C $repo init --quiet
if ($LASTEXITCODE) { throw 'fixture git init failed' }
& $git -C $repo add .
if ($LASTEXITCODE) { throw 'fixture git add failed' }
& $git -C $repo -c user.name=fixture -c user.email=fixture@example.invalid commit --quiet -m fixture
if ($LASTEXITCODE) { throw 'fixture git commit failed' }
function Assert-True([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
function global:go {
    $arguments = @($args)
    $global:LASTEXITCODE=0
    if ($arguments[0] -eq 'version') { 'go version go1.26.7 linux/amd64'; return }
    if ($arguments[0] -eq 'env') { if ($arguments[1] -eq 'GOHOSTOS') { 'linux' } else { 'amd64' }; return }
    if ($arguments[0] -ne 'build') { throw 'unexpected fake compiler command' }
    $global:ZhBuildTest.BuildCount++
    Assert-True ($env:CGO_ENABLED -eq '0' -and $env:GOAMD64 -eq 'v1') 'compiler env is not explicit'
    Assert-True ($arguments -contains '-buildvcs=false' -and $arguments -contains '-trimpath' -and $arguments -contains 'with_gvisor') 'compiler flags missing'
    $flags = $arguments[[Array]::IndexOf($arguments,'-ldflags')+1]
    Assert-True ($flags -match 'SourceCommit=[a-f0-9]{40}' -and $flags -match 'SourceState=(dirty|clean)') 'compiler source identity missing'
    if ($global:ZhBuildTest.Mode -eq 'build-fails') { $global:LASTEXITCODE=17; return }
    $out = $arguments[[Array]::IndexOf($arguments,'-o')+1]
    [IO.File]::WriteAllBytes($out,[Text.Encoding]::UTF8.GetBytes('synthetic binary '+$env:GOOS+'/'+$env:GOARCH))
    if ($global:ZhBuildTest.Mode -eq 'build-source-change' -and $global:ZhBuildTest.BuildCount -eq 2) { Add-Content -LiteralPath (Join-Path $global:ZhBuildTest.Repository 'README.md') -Value 'changed during compile' }
}
$originalEnvironment = @{}
foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) { $originalEnvironment[$key]=[Environment]::GetEnvironmentVariable($key,'Process') }
$passed=0
try {
    foreach ($case in @('release','invalid-version','existing-output','gate-fails','gate-source-change','build-fails','build-source-change','positive-windows','positive-macos')) {
        $global:ZhBuildTest = @{Mode=$case;Repository=$repo;GateCount=0;BuildCount=0}
        $out = Join-Path $fixture $case
        & $git -C $repo checkout -- README.md
        if ($LASTEXITCODE) { throw 'fixture reset failed' }
        if ($case -eq 'existing-output') { New-Item -ItemType Directory -Path $out | Out-Null; Set-Content -LiteralPath (Join-Path $out 'preserved.txt') -Value 'preserve' }
        $expectedEnv = @{GOOS='fixture-os';GOARCH='fixture-arch';GOAMD64='fixture-amd64';CGO_ENABLED='fixture-cgo'}
        foreach ($key in $expectedEnv.Keys) { [Environment]::SetEnvironmentVariable($key,$expectedEnv[$key],'Process') }
        $params = @{Platform=$(if ($case -eq 'positive-macos') { 'macos' } else { 'windows' });OutputDirectory=$out;Development=($case -ne 'release');Version=$(if ($case -eq 'invalid-version') { 'bad version' } else { '0.1.2' })}
        $failed=$false
        try { & (Join-Path $scripts 'build-cli.ps1') @params } catch { $failed=$true }
        foreach ($key in $expectedEnv.Keys) { Assert-True ([Environment]::GetEnvironmentVariable($key,'Process') -ceq $expectedEnv[$key]) "env leaked in $case ($key)" }
        if ($case.StartsWith('positive-')) {
            Assert-True (-not $failed) "positive build failed: $case"
            $manifest = Get-Content -LiteralPath (Join-Path $out 'build-manifest.json') -Raw | ConvertFrom-Json
            Assert-True ($manifest.source_commit -match '^[a-f0-9]{40}$' -and $manifest.source_state -eq 'clean') 'manifest source identity missing'
            Assert-True ($manifest.development -and -not $manifest.signed -and -not $manifest.release_ready -and $manifest.channel -eq 'development') 'unsigned build falsely advertised release'
            Assert-True ($global:ZhBuildTest.GateCount -eq 1 -and $global:ZhBuildTest.BuildCount -eq 2 -and @($manifest.artifacts).Count -eq 2) 'missing unified gate or target'
            foreach ($entry in $manifest.artifacts) { Assert-True ((Get-FileHash -LiteralPath (Join-Path $out $entry.file) -Algorithm SHA256).Hash.ToLowerInvariant() -eq $entry.sha256 -and $entry.acceptance -eq 'compile_only' -and -not $entry.signed) 'artifact hash/acceptance wrong' }
            Assert-True ((Get-FileHash -LiteralPath (Join-Path $out 'source-manifest.json') -Algorithm SHA256).Hash.ToLowerInvariant() -eq $manifest.source_manifest_sha256) 'source manifest hash wrong'
        } else {
            Assert-True $failed "refusal missing: $case"
            Assert-True (-not (Test-Path -LiteralPath (Join-Path $out 'build-manifest.json'))) "false success manifest: $case"
            if ($case -in @('release','invalid-version','existing-output','gate-fails','gate-source-change')) { Assert-True ($global:ZhBuildTest.BuildCount -eq 0) "compiler ran after refusal: $case" }
            if ($case -in @('release','invalid-version')) { Assert-True (-not (Test-Path -LiteralPath $out)) 'release/invalid input created output' }
            if ($case -eq 'existing-output') { Assert-True ((Get-Content -LiteralPath (Join-Path $out 'preserved.txt')).Trim() -eq 'preserve') 'existing artifact changed' }
        }
        $passed++
        Write-Host "PASS CLI builder case: $case"
    }
} finally {
    Remove-Item -LiteralPath Function:\go -ErrorAction SilentlyContinue
    foreach ($key in $originalEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$originalEnvironment[$key],'Process') }
    Remove-Variable -Name ZhBuildTest -Scope Global -ErrorAction SilentlyContinue
}
Write-Host "CLI builder behavior passed: $passed cases. Synthetic compiler/gate evidence retained: $fixture"
