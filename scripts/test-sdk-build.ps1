# The actual SDK builder runs against isolated Git source. Gate/compiler/pip
# are doubles; version is a real owned native child. No product is compiled.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'SDK native version refusal fixtures require Windows.' }
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-sdk-builder-tests-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$compiler = Join-Path $env:WINDIR 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) { throw 'Owned native fixture compiler unavailable.' }
$nativeSource = Join-Path $fixture 'owned-version.cs'
$nativeImage = Join-Path $fixture 'owned-version.exe'
@'
using System;
using System.IO;
public class OwnedVersion {
    public static int Main(string[] args) {
        File.AppendAllText(Environment.GetEnvironmentVariable("ZHSDK_TEST_CHILD_RECEIPT"), "version-child\n");
        if (args.Length != 2 || args[0] != "version" || args[1] != "--json") return 51;
        string product = Environment.GetEnvironmentVariable("ZHSDK_TEST_MODE") == "identity-mismatch" ? "wrong-product" : "python-sdk";
        Console.WriteLine("{\"product\":\""+product+"\",\"version\":\"0.1.2\",\"protocol_version\":2,\"source_commit\":\""+
            Environment.GetEnvironmentVariable("ZHSDK_TEST_SOURCE_COMMIT")+"\",\"source_state\":\""+
            Environment.GetEnvironmentVariable("ZHSDK_TEST_SOURCE_STATE")+"\"}");
        return Environment.GetEnvironmentVariable("ZHSDK_TEST_MODE") == "version-nonzero" ? 19 : 0;
    }
}
'@ | Set-Content -LiteralPath $nativeSource -Encoding utf8
& $compiler /nologo /target:exe /platform:x64 "/out:$nativeImage" $nativeSource
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $nativeImage)) { throw 'Native version fixture compilation failed.' }
$git = @(Get-Command git -CommandType Application)[0].Source
function Assert-True([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
function global:go {
    $arguments = @($args)
    $global:LASTEXITCODE=0
    if ($arguments[0] -eq 'version' -or $arguments[0] -eq 'env') {
        foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) {
            Assert-True ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($key,'Process'))) "Inherited compiler env reached host discovery ($key)"
        }
        if ($arguments[0] -eq 'version') { 'go version go1.26.7 windows/amd64'; return }
        if ($arguments[1] -eq 'GOHOSTOS') { 'windows'; return }
        if ($arguments[1] -eq 'GOHOSTARCH') { 'amd64'; return }
        throw 'Builder used cross-build GOOS/GOARCH as host identity'
    }
    if ($arguments[0] -ne 'build') { throw 'Unexpected fixture compiler command' }
    $global:ZhSDKBuildTest.BuildCount++
    Assert-True ($env:GOOS -eq 'windows' -and $env:GOARCH -eq 'amd64' -and $env:GOAMD64 -eq 'v1' -and $env:CGO_ENABLED -eq '0') 'Explicit SDK compiler environment missing'
    Assert-True ($arguments -contains '-trimpath' -and $arguments -contains '-buildvcs=false' -and $arguments -contains 'with_gvisor') 'SDK compiler flags missing'
    $flags = $arguments[[Array]::IndexOf($arguments,'-ldflags')+1]
    Assert-True ($flags -match 'SourceCommit=([a-f0-9]{40})') 'SDK source SHA missing'
    $env:ZHSDK_TEST_SOURCE_COMMIT=$Matches[1]
    Assert-True ($flags -match 'SourceState=(clean|dirty)') 'SDK source state missing'
    $env:ZHSDK_TEST_SOURCE_STATE=$Matches[1]
    if ($global:ZhSDKBuildTest.Mode -eq 'build-fails') { $global:LASTEXITCODE=17; return }
    $out = $arguments[[Array]::IndexOf($arguments,'-o')+1]
    Copy-Item -LiteralPath $global:ZhSDKBuildTest.NativeImage -Destination $out
    if ($global:ZhSDKBuildTest.Mode -eq 'build-source-change') { Add-Content -LiteralPath (Join-Path $global:ZhSDKBuildTest.Repository 'README.md') -Value 'changed during compile' }
}
function global:python {
    Assert-True (@($args)[0] -eq '-m' -and @($args)[1] -eq 'pip') 'Unexpected fixture installer command'
    $global:ZhSDKBuildTest.InstallCount++
    $global:LASTEXITCODE=$(if ($global:ZhSDKBuildTest.Mode -eq 'pip-fails') { 31 } else { 0 })
    if ($global:ZhSDKBuildTest.Mode -eq 'install-source-change') { Add-Content -LiteralPath (Join-Path $global:ZhSDKBuildTest.Repository 'README.md') -Value 'changed during install' }
}
$environmentKeys = @('GOOS','GOARCH','GOAMD64','CGO_ENABLED','ZHSDK_TEST_MODE','ZHSDK_TEST_SOURCE_COMMIT','ZHSDK_TEST_SOURCE_STATE','ZHSDK_TEST_CHILD_RECEIPT')
$originalEnvironment = @{}
foreach ($key in $environmentKeys) { $originalEnvironment[$key]=[Environment]::GetEnvironmentVariable($key,'Process') }
$passed=0
try {
    foreach ($case in @('release','wheel','existing-exe','existing-build-manifest','existing-source-manifest','gate-throws','gate-nonzero','gate-source-change','build-fails','build-source-change','identity-mismatch','version-nonzero','pip-fails','install-source-change','positive','positive-install')) {
        $repo = Join-Path $fixture $case
        $scripts = Join-Path $repo 'scripts'
        $sdk = Join-Path $repo 'sdk/python'
        $bin = Join-Path $sdk 'src/zongheng_vpn/bin'
        New-Item -ItemType Directory -Path $scripts,$bin | Out-Null
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'source-manifest.ps1') -Destination (Join-Path $scripts 'source-manifest.ps1')
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot '../sdk/python/build.ps1') -Destination (Join-Path $sdk 'build.ps1')
        Set-Content -LiteralPath (Join-Path $sdk 'pyproject.toml') -Value "version = `"0.1.2`"" -Encoding utf8
        Set-Content -LiteralPath (Join-Path $repo 'README.md') -Value 'fixture source' -Encoding utf8
        Set-Content -LiteralPath (Join-Path $repo '.gitignore') -Value @('*.exe','sdk/python/src/zongheng_vpn/bin/build-manifest.json','sdk/python/src/zongheng_vpn/bin/source-manifest.json') -Encoding utf8
@'
param([Parameter(Mandatory=$true)][string]$EvidenceDirectory)
$global:ZhSDKBuildTest.GateCount++
foreach ($key in @('GOOS','GOARCH','GOAMD64','CGO_ENABLED')) {
    Assert-True ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($key,'Process'))) "Inherited cross-compiler env reached local gate ($key)"
}
if ($global:ZhSDKBuildTest.Mode -eq 'gate-throws') { throw 'synthetic gate rejection' }
if ($global:ZhSDKBuildTest.Mode -eq 'gate-nonzero') { $global:LASTEXITCODE=23; return }
New-Item -ItemType Directory -Path $EvidenceDirectory | Out-Null
Set-Content -LiteralPath (Join-Path $EvidenceDirectory 'gate.txt') -Value 'synthetic gate receipt'
if ($global:ZhSDKBuildTest.Mode -eq 'gate-source-change') { Add-Content -LiteralPath (Join-Path $global:ZhSDKBuildTest.Repository 'README.md') -Value 'changed during gate' }
$global:LASTEXITCODE=0
'@ | Set-Content -LiteralPath (Join-Path $scripts 'check-steelman.ps1') -Encoding utf8
        & $git -C $repo init --quiet
        if ($LASTEXITCODE) { throw 'Fixture git init failed' }
        & $git -C $repo add .
        if ($LASTEXITCODE) { throw 'Fixture git add failed' }
        & $git -C $repo -c user.name=fixture -c user.email=fixture@example.invalid commit --quiet -m fixture
        if ($LASTEXITCODE) { throw 'Fixture git commit failed' }
        $global:ZhSDKBuildTest=@{Mode=$case;Repository=$repo;GateCount=0;BuildCount=0;InstallCount=0;NativeImage=$nativeImage}
        $env:ZHSDK_TEST_MODE=$case
        $env:ZHSDK_TEST_CHILD_RECEIPT=Join-Path $fixture ($case+'-native-child.txt')
        $existingName = switch ($case) { 'existing-exe' { 'zhvpn.exe' }; 'existing-build-manifest' { 'build-manifest.json' }; 'existing-source-manifest' { 'source-manifest.json' }; default { $null } }
        if ($existingName) { [IO.File]::WriteAllBytes((Join-Path $bin $existingName),[Text.Encoding]::UTF8.GetBytes('preserve existing artifact')) }
        $expectedEnvironment=@{GOOS='fixture-cross-os';GOARCH='fixture-cross-arch';GOAMD64='fixture-amd64';CGO_ENABLED='fixture-cgo'}
        foreach ($key in $expectedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$expectedEnvironment[$key],'Process') }
        $params=@{Target='host';Development=($case -ne 'release');Wheel=($case -eq 'wheel');Install=($case -in @('pip-fails','install-source-change','positive-install'))}
        $failed=$false
        try { & (Join-Path $sdk 'build.ps1') @params } catch { $failed=$true }
        foreach ($key in $expectedEnvironment.Keys) { Assert-True ([Environment]::GetEnvironmentVariable($key,'Process') -ceq $expectedEnvironment[$key]) "Compiler env leaked after $case ($key)" }
        if ($case -in @('positive','positive-install')) {
            Assert-True (-not $failed) "Positive SDK fixture failed: $case"
            $manifest=Get-Content -LiteralPath (Join-Path $bin 'build-manifest.json') -Raw | ConvertFrom-Json
            Assert-True ($manifest.product -eq 'python-sdk' -and $manifest.target -eq 'windows-amd64' -and $manifest.source_commit -match '^[a-f0-9]{40}$' -and $manifest.source_state -eq 'clean' -and $manifest.development -and -not $manifest.signed) 'SDK manifest identity/target incorrect'
            Assert-True ($manifest.sha256 -eq (Get-FileHash -LiteralPath (Join-Path $bin 'zhvpn.exe') -Algorithm SHA256).Hash.ToLowerInvariant()) 'SDK artifact hash incorrect'
            Assert-True (Test-Path -LiteralPath (Join-Path $bin 'source-manifest.json')) 'SDK source receipt absent'
            Assert-True ($global:ZhSDKBuildTest.GateCount -eq 1 -and $global:ZhSDKBuildTest.BuildCount -eq 1) 'Gate/compiler did not run once'
            Assert-True ((Get-Content -LiteralPath $env:ZHSDK_TEST_CHILD_RECEIPT).Count -eq 1) 'Native version child not executed exactly once'
            Assert-True ($global:ZhSDKBuildTest.InstallCount -eq $(if ($case -eq 'positive-install') { 1 } else { 0 })) 'Optional installer call incorrect'
            Assert-True (@(& $git -C $repo status --porcelain).Count -eq 0) 'Generated receipts polluted source state'
        } else {
            Assert-True $failed "SDK refusal missing: $case"
            foreach ($name in @('build-manifest.json','source-manifest.json')) {
                if ($name -ne $existingName) { Assert-True (-not (Test-Path -LiteralPath (Join-Path $bin $name))) "False success receipt survived $case ($name)" }
            }
            if ($existingName) {
                Assert-True ([IO.File]::ReadAllText((Join-Path $bin $existingName)) -ceq 'preserve existing artifact') "Existing artifact changed in $case"
                Assert-True ($global:ZhSDKBuildTest.BuildCount -eq 0 -and $global:ZhSDKBuildTest.GateCount -eq 0) 'Existing output was reused by gate/compiler'
            }
            if ($case -in @('release','wheel','gate-throws','gate-nonzero')) { Assert-True ($global:ZhSDKBuildTest.BuildCount -eq 0) "Compiler ran after refusal in $case" }
            if ($case -in @('identity-mismatch','version-nonzero','pip-fails','install-source-change')) { Assert-True ((Get-Content -LiteralPath $env:ZHSDK_TEST_CHILD_RECEIPT).Count -eq 1) "Native version refusal was not exercised in $case" }
            if ($case -eq 'pip-fails') { Assert-True ($global:ZhSDKBuildTest.InstallCount -eq 1) 'Installer failure was not exercised' }
        }
        $passed++
        Write-Host "PASS SDK builder case: $case"
    }
} finally {
    Remove-Item -LiteralPath Function:\go,Function:\python -ErrorAction SilentlyContinue
    foreach ($key in $environmentKeys) { [Environment]::SetEnvironmentVariable($key,$originalEnvironment[$key],'Process') }
    Remove-Variable -Name ZhSDKBuildTest -Scope Global -ErrorAction SilentlyContinue
}
Write-Host "SDK builder behavior passed: $passed cases. Synthetic compiler/gate/install and actual native version-child evidence retained: $fixture"
