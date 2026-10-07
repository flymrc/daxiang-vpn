[CmdletBinding()]
param([string]$EvidenceDirectory = (Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-build-refusal-' + [guid]::NewGuid().ToString('N'))))
$ErrorActionPreference = 'Stop'
if (Test-Path -LiteralPath $EvidenceDirectory) { throw 'Build refusal fixture requires a new evidence directory.' }
New-Item -ItemType Directory -Path $EvidenceDirectory | Out-Null
$fixture = [IO.Path]::GetFullPath($EvidenceDirectory)
$repo = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '../../..')).Path
$desktop = Join-Path $fixture 'clients/desktop-gui'
$scripts = Join-Path $fixture 'scripts'
New-Item -ItemType Directory -Path $scripts,(Join-Path $desktop 'src-tauri'),(Join-Path $desktop 'node_modules/@tauri-apps/cli') | Out-Null
foreach ($file in @('build.ps1','package.json','package-lock.json','src-tauri/Cargo.toml','src-tauri/tauri.conf.json','node_modules/@tauri-apps/cli/package.json')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "../$file") -Destination (Join-Path $desktop $file)
}
foreach ($file in @('check-steelman.ps1','source-manifest.ps1')) {
    Copy-Item -LiteralPath (Join-Path $repo "scripts/$file") -Destination (Join-Path $scripts $file)
}
# Exercise the real unified gate's rejection branch. Tool/scanner producers
# are controlled fixture doubles; no compiler, signing or product is run.
'$global:LASTEXITCODE = 0' | Set-Content -LiteralPath (Join-Path $scripts 'check-client-safety.ps1') -Encoding utf8
@'
param([string]$EvidenceDirectory)
if ([string]::IsNullOrWhiteSpace($EvidenceDirectory) -or (Test-Path -LiteralPath $EvidenceDirectory)) { throw 'Builder did not use fresh gate evidence.' }
'scanner rejected' | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'scanner-rejected.txt')
$global:LASTEXITCODE = 23
'@ | Set-Content -LiteralPath (Join-Path $scripts 'check-security.ps1') -Encoding utf8
$harness = Join-Path $fixture 'owned-harness.ps1'
@'
$ErrorActionPreference = 'Stop'
function global:git {
    $global:LASTEXITCODE = 0
    if ($args -contains 'rev-parse') { return ('f' * 40) }
    if ($args -contains 'status') { return ' M synthetic-source' }
    if ($args -contains 'ls-files') { return ('clients/desktop-gui/build.ps1' + [char]0 + 'scripts/check-steelman.ps1' + [char]0) }
    throw 'Unexpected git fixture request'
}
function global:rustc { $global:LASTEXITCODE = 0; 'host: x86_64-pc-windows-msvc' }
function global:rustup { $global:LASTEXITCODE = 0; 'x86_64-pc-windows-msvc' }
function global:go {
    $global:LASTEXITCODE = 0
    if ($args[0] -ne 'env') { throw 'Product build reached after scanner rejection' }
    if ($args[1] -eq 'GOOS') { 'windows' } elseif ($args[1] -eq 'GOARCH') { 'amd64' } else { throw 'Unexpected Go fixture request' }
}
function global:npm {
    $global:LASTEXITCODE = 0
    if ($args[0] -ne 'ci') { throw 'Product bundle reached after scanner rejection' }
}
Remove-Item Env:CARGO_TARGET_DIR -ErrorAction SilentlyContinue
try { & (Join-Path $PSScriptRoot 'clients/desktop-gui/build.ps1') -Development; throw 'Rejected scan was accepted by builder' }
catch {
    if ($_.Exception.Message -ne 'Security gate failed.') { throw }
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'scripts/scanner-rejected.txt'))) { throw 'Scanner rejection was not exercised' }
    if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'clients/desktop-gui/src-tauri/binaries')) { throw 'Sidecar was created after gate rejection' }
    if (@(Get-ChildItem -LiteralPath $PSScriptRoot -Recurse -File -Filter '*manifest.json').Count -ne 0) { throw 'A success manifest survived gate rejection' }
    [Console]::WriteLine('Security gate rejection stops the real inner builder before product outputs or success manifests.')
}
'@ | Set-Content -LiteralPath $harness -Encoding utf8
$info = [Diagnostics.ProcessStartInfo]::new((Get-Command pwsh -ErrorAction Stop).Source)
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.ArgumentList.Add('-NoProfile')
$info.ArgumentList.Add('-File')
$info.ArgumentList.Add($harness)
$process = [Diagnostics.Process]::Start($info)
try {
    if (-not $process.WaitForExit(15000) -or $process.ExitCode -ne 0) { throw 'Owned build failure fixture failed.' }
} finally { $process.Dispose() }
Write-Host "Build failure fixture passed: $fixture"
