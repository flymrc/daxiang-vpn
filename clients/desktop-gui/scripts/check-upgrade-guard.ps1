[CmdletBinding()]
param(
    [string]$Makensis = (Join-Path $env:LOCALAPPDATA 'tauri/NSIS/makensis.exe'),
    [string]$EvidenceDirectory = (Join-Path ([Environment]::GetFolderPath('UserProfile')) ('zhvpn-fresh-install-' + [guid]::NewGuid().ToString('N')))
)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'Fresh-install checks require Windows.' }
if (-not (Test-Path -LiteralPath $Makensis -PathType Leaf)) { throw "NSIS compiler not found: $Makensis" }
if (Test-Path -LiteralPath $EvidenceDirectory) { throw 'Use a new evidence directory.' }
New-Item -ItemType Directory -Path $EvidenceDirectory | Out-Null
$evidenceRoot = (Resolve-Path -LiteralPath $EvidenceDirectory).Path
# This ACL applies only to the newly created owned fixture, never a profile or
# installation directory. Production guards must refuse an untrusted parent.
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = [Security.AccessControl.DirectorySecurity]::new()
$acl.SetOwner($sid)
$acl.SetAccessRuleProtection($true, $false)
foreach ($identity in @($sid, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'), [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
}
Set-Acl -LiteralPath $evidenceRoot -AclObject $acl
python (Join-Path $PSScriptRoot 'installer-guardgen.py') -check
if ($LASTEXITCODE -ne 0) { throw 'Embedded installer validators are stale.' }
$lockedVersion = (Get-Content -LiteralPath (Join-Path $PSScriptRoot '../package-lock.json') -Raw | ConvertFrom-Json -AsHashtable)['packages']['node_modules/@tauri-apps/cli']['version']
if ($lockedVersion -ne '2.11.2') { throw 'The pinned Tauri template/CLI versions diverged.' }
$installedVersion = (Get-Content -LiteralPath (Join-Path $PSScriptRoot '../node_modules/@tauri-apps/cli/package.json') -Raw | ConvertFrom-Json).version
if ($installedVersion -ne $lockedVersion) { throw 'Install the pinned Tauri CLI before validating the custom template.' }
$hook = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '../src-tauri/installer-hooks.nsh')).Path
# An owned overlay introduces a barrier immediately before the actual Rename.
# The atomic primitive and all guards remain the production instructions.
$overlayHook = Join-Path $evidenceRoot 'installer-hooks-overlay.nsh'
$hookText = Get-Content -LiteralPath $hook -Raw
$includePath = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '../src-tauri')).Path
$hookText = $hookText.Replace('${__FILEDIR__}\installer-validation.generated.nsh', "$includePath\installer-validation.generated.nsh")
$hookText = $hookText.Replace('  Rename "$ZhPayloadStage" "$INSTDIR"', @'
  ${If} $Mode == "rename-race"
    FileOpen $R8 "$EXEDIR\rename-prepared.flag" w
    FileWrite $R8 "last check complete"
    FileClose $R8
    Sleep 1500
  ${EndIf}
  Rename "$ZhPayloadStage" "$INSTDIR"
'@)
$hookText | Set-Content -LiteralPath $overlayHook -Encoding utf8
$hook = $overlayHook
$template = Get-Content -LiteralPath (Join-Path $PSScriptRoot '../src-tauri/installer-fresh-only.nsi') -Raw
$config = Get-Content -LiteralPath (Join-Path $PSScriptRoot '../src-tauri/tauri.conf.json') -Raw | ConvertFrom-Json
if ($config.bundle.windows.nsis.template -ne 'installer-fresh-only.nsi') { throw 'Fresh-only template is not selected.' }
if ($template -match 'PageReinstall|PageLeaveReinstall|reinst_uninstall' -or $template -match "ExecWait '\`$R1'") { throw 'The template contains an old-uninstaller maintenance path.' }
$init = [regex]::Match($template, '(?s)Function \.onInit\s+(.*?)FunctionEnd').Groups[1].Value
if ($init -notmatch 'ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION' -or $init -notmatch 'ZHVPN_REQUIRE_FRESH_TARGET') { throw 'Maintenance must be refused in .onInit.' }
$install = [regex]::Match($template, '(?s)Section Install\s+(.*?)SectionEnd').Groups[1].Value
if ($install -notmatch 'ZHVPN_CREATE_FRESH_STAGE' -or $install -notmatch 'ZHVPN_PUBLISH_FRESH_PAYLOAD' -or $install -match 'SetOutPath \`$INSTDIR') { throw 'Payload must be staged and published as one directory.' }
$source = Join-Path $evidenceRoot 'owned-child.rs'
'fn main() { let mut line = String::new(); let _ = std::io::stdin().read_line(&mut line); }' | Set-Content -LiteralPath $source -Encoding utf8
$childImage = Join-Path $evidenceRoot 'owned-child.exe'
rustc --crate-name owned_fresh_install_child $source -o $childImage
if ($LASTEXITCODE -ne 0) { throw 'Synthetic owned child compilation failed.' }
$guiPayload = Join-Path $evidenceRoot 'zhvpn-desktop.exe'
$cliPayload = Join-Path $evidenceRoot 'zhvpn.exe'
Copy-Item -LiteralPath $childImage -Destination $guiPayload
Copy-Item -LiteralPath $childImage -Destination $cliPayload
$guiHash = (Get-FileHash -LiteralPath $guiPayload -Algorithm SHA256).Hash
$cliHash = (Get-FileHash -LiteralPath $cliPayload -Algorithm SHA256).Hash
$probeInstaller = Join-Path $evidenceRoot 'fresh-probe.exe'
$nsi = Join-Path $evidenceRoot 'fresh-probe.nsi'
@"
Unicode true
Name "Synthetic fresh-only install guard"
OutFile "$probeInstaller"
RequestExecutionLevel user
SilentInstall silent
!include "FileFunc.nsh"
!define ZHVPN_GUI_SHA256 "$guiHash"
!define ZHVPN_CLI_SHA256 "$cliHash"
!include "$hook"
; Synthetic registry discovery only: this probe never reads/writes real HKCU.
!macroundef ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION
!macro ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION
  `${If} `${FileExists} "`$EXEDIR\synthetic-registration.flag"
    !insertmacro ZHVPN_REFUSE_EXISTING_REGISTRATION "synthetic registered install"
  `${EndIf}
!macroend
Var Mode
Function .onInit
  `${GetOptions} `$CMDLINE "/CASE=" `$Mode
  !insertmacro ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION
  !insertmacro ZHVPN_REQUIRE_FRESH_TARGET
  ; Simulates the first old-uninstaller page; it must never be reached on refusal.
  FileOpen `$0 "`$EXEDIR\`$Mode.init-passed" w
  FileWrite `$0 "old-uninstaller boundary was reached"
  FileClose `$0
FunctionEnd
Section
  InitPluginsDir
  !insertmacro NSIS_HOOK_PREINSTALL
  !insertmacro CheckIfAppIsRunning "zhvpn-desktop.exe" "synthetic"
  !insertmacro ZHVPN_CREATE_FRESH_STAGE
  ClearErrors
  File /oname=zhvpn-desktop.exe "$guiPayload"
  !insertmacro ZHVPN_REQUIRE_EXTRACTION_SUCCESS
  `${If} `$Mode != "missing-cli"
    ClearErrors
    File /a /oname=zhvpn.exe "$cliPayload"
    !insertmacro ZHVPN_REQUIRE_EXTRACTION_SUCCESS
  `${EndIf}
  `${If} `$Mode == "extract-failure"
    SetErrors
    !insertmacro ZHVPN_REQUIRE_EXTRACTION_SUCCESS
  `${EndIf}
  `${If} `$Mode == "tampered-cli"
    FileOpen `$0 "`$ZhPayloadStage\zhvpn.exe" w
    FileWrite `$0 "synthetic corruption"
    FileClose `$0
  `${EndIf}
  `${If} `$Mode == "stage-pin"
    FileOpen `$0 "`$EXEDIR\stage-path.txt" w
    FileWrite `$0 "`$ZhPayloadStage"
    FileClose `$0
    Sleep 1500
  `${EndIf}
  `${If} `$Mode == "target-race"
    FileOpen `$0 "`$EXEDIR\race-prepared.flag" w
    FileWrite `$0 "complete payload staged"
    FileClose `$0
    Sleep 1000
  `${EndIf}
  !insertmacro ZHVPN_PUBLISH_FRESH_PAYLOAD
  SetErrorLevel 0
SectionEnd
"@ | Set-Content -LiteralPath $nsi -Encoding utf8
& $Makensis /INPUTCHARSET UTF8 /NOCD /V2 $nsi
if ($LASTEXITCODE -ne 0) { throw 'NSIS guard probe compilation failed.' }
function Start-OwnedChild([string]$Path) {
    $info = [Diagnostics.ProcessStartInfo]::new($Path)
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $process = [Diagnostics.Process]::Start($info)
    if ($process.HasExited) { throw 'Synthetic child exited early.' }
    return $process
}
function Stop-OwnedChild([Diagnostics.Process]$Process) {
    if ($null -eq $Process) { return }
    $Process.StandardInput.Close()
    if (-not $Process.WaitForExit(5000)) { throw 'Owned child did not stop after closing its input.' }
    $Process.Dispose()
}
function Start-Probe([string]$Case, [string]$Installation) {
    $info = [Diagnostics.ProcessStartInfo]::new($probeInstaller)
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.ArgumentList.Add('/S')
    $info.ArgumentList.Add('/CASE=' + $Case)
    $info.ArgumentList.Add('/D=' + $Installation)
    return [Diagnostics.Process]::Start($info)
}
function Complete-Probe([Diagnostics.Process]$Process) {
    try {
        if (-not $Process.WaitForExit(15000)) { throw 'Synthetic probe exceeded its budget.' }
        return $Process.ExitCode
    } finally { $Process.Dispose() }
}
$rows = [Collections.Generic.List[object]]::new()
$other = $null
$current = $null
try {
    $other = Start-OwnedChild $childImage
    $fresh = Join-Path $evidenceRoot 'fresh-install'
    $exit = Complete-Probe (Start-Probe 'fresh' $fresh)
    if ($exit -ne 0 -or $other.HasExited -or -not (Test-Path -LiteralPath (Join-Path $fresh 'zhvpn-desktop.exe')) -or -not (Test-Path -LiteralPath (Join-Path $fresh 'zhvpn.exe'))) { throw 'Fresh directory did not publish a complete pair without affecting unrelated child.' }
    $rows.Add(@{ case='fresh pair published atomically'; exit_code=$exit; unrelated_child_alive=(-not $other.HasExited) })

    $exit = Complete-Probe (Start-Probe 'existing-inactive' $fresh)
    if ($exit -eq 0 -or (Test-Path -LiteralPath (Join-Path $evidenceRoot 'existing-inactive.init-passed'))) { throw 'Existing target reached the old-uninstaller boundary.' }
    $rows.Add(@{ case='existing inactive target refuses before old uninstall'; exit_code=$exit })

    $current = Start-OwnedChild (Join-Path $fresh 'zhvpn.exe')
    $exit = Complete-Probe (Start-Probe 'existing-active' $fresh)
    if ($exit -eq 0 -or $current.HasExited -or $other.HasExited -or (Test-Path -LiteralPath (Join-Path $evidenceRoot 'existing-active.init-passed'))) { throw 'Active target was not refused before maintenance, or an owned child was stopped.' }
    $rows.Add(@{ case='existing active target refuses before old uninstall'; exit_code=$exit; child_alive=(-not $current.HasExited) })
    Stop-OwnedChild $current
    $current=$null

    $registration = Join-Path $evidenceRoot 'synthetic-registration.flag'
    'synthetic registration' | Set-Content -LiteralPath $registration
    $registeredTarget = Join-Path $evidenceRoot 'registered-new-target'
    $exit = Complete-Probe (Start-Probe 'registered' $registeredTarget)
    if ($exit -eq 0 -or (Test-Path -LiteralPath $registeredTarget) -or (Test-Path -LiteralPath (Join-Path $evidenceRoot 'registered.init-passed'))) { throw 'A synthetic registered install reached old uninstall.' }
    $rows.Add(@{ case='registered install refuses before old uninstall'; exit_code=$exit })
    Remove-Item -LiteralPath $registration

    $empty = Join-Path $evidenceRoot 'existing-empty'
    New-Item -ItemType Directory -Path $empty | Out-Null
    $exit = Complete-Probe (Start-Probe 'existing-empty' $empty)
    if ($exit -eq 0 -or (Get-ChildItem -LiteralPath $empty -Force).Count -ne 0) { throw 'Empty existing directory was adopted.' }
    $rows.Add(@{ case='existing empty target is not adopted'; exit_code=$exit })

    foreach ($case in @('missing-cli','extract-failure','tampered-cli')) {
        $target = Join-Path $evidenceRoot $case
        $exit = Complete-Probe (Start-Probe $case $target)
        if ($exit -eq 0 -or (Test-Path -LiteralPath $target)) { throw "Incomplete payload published: $case" }
        $rows.Add(@{ case=$case; exit_code=$exit; target_absent=$true })
    }

    $raceTarget=Join-Path $evidenceRoot 'raced-install'
    $race=Start-Probe 'target-race' $raceTarget
    try {
        $deadline=[DateTime]::UtcNow.AddSeconds(5)
        while (-not (Test-Path -LiteralPath (Join-Path $evidenceRoot 'race-prepared.flag')) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
        if (-not (Test-Path -LiteralPath (Join-Path $evidenceRoot 'race-prepared.flag'))) { throw 'Race probe did not reach staging barrier.' }
        if (Test-Path -LiteralPath $raceTarget) { throw 'Public target existed before payload publication.' }
        New-Item -ItemType Directory -Path $raceTarget | Out-Null
        $marker=Join-Path $raceTarget 'unrelated-owner.txt'
        'must survive' | Set-Content -LiteralPath $marker
        $exit=Complete-Probe $race
        $race=$null
        if ($exit -eq 0 -or (Get-Content -LiteralPath $marker -Raw).Trim() -ne 'must survive' -or (Test-Path -LiteralPath (Join-Path $raceTarget 'zhvpn.exe')) -or (Test-Path -LiteralPath (Join-Path $raceTarget 'zhvpn-desktop.exe'))) { throw 'Late target creation was overwritten or combined with a partial payload.' }
        $rows.Add(@{ case='late target creation refuses complete publish'; exit_code=$exit; unrelated_target_unchanged=$true })
    } finally { if ($null -ne $race) { $race.WaitForExit(15000) | Out-Null; $race.Dispose() } }

    # A hostile empty target created after the last precheck still must not
    # be replaced by Rename. This covers the exact publication race window.
    $renameTarget = Join-Path $evidenceRoot 'rename-raced-install'
    $race = Start-Probe 'rename-race' $renameTarget
    try {
        $barrier = Join-Path $evidenceRoot 'rename-prepared.flag'
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        while (-not (Test-Path -LiteralPath $barrier) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
        if (-not (Test-Path -LiteralPath $barrier)) { throw 'Probe did not reach the final Rename barrier.' }
        New-Item -ItemType Directory -Path $renameTarget | Out-Null
        $exit = Complete-Probe $race
        $race = $null
        if ($exit -eq 0 -or @(Get-ChildItem -LiteralPath $renameTarget -Force).Count -ne 0) { throw 'Atomic Rename replaced an empty existing target.' }
        $rows.Add(@{ case='creation after final precheck refuses atomic rename'; exit_code=$exit; empty_target_unchanged=$true })
    } finally { if ($null -ne $race) { $race.WaitForExit(15000) | Out-Null; $race.Dispose() } }

    # Observe the real private stage and try renaming both it and its parent
    # while the installer holds native handles. Only owned fixtures are used.
    $pinnedTarget = Join-Path $evidenceRoot 'pinned-install'
    $pinned = Start-Probe 'stage-pin' $pinnedTarget
    try {
        $stageFile = Join-Path $evidenceRoot 'stage-path.txt'
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        while (-not (Test-Path -LiteralPath $stageFile) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
        if (-not (Test-Path -LiteralPath $stageFile)) { throw 'Probe did not reach pinned stage.' }
        $stage = Get-Content -LiteralPath $stageFile -Raw
        $stageAcl = Get-Acl -LiteralPath $stage
        if (-not $stageAcl.AreAccessRulesProtected -or $stageAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid.Value) { throw 'Stage does not have explicit private ownership/DACL.' }
        foreach ($ace in $stageAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
            if ($ace.IdentityReference.Value -notin @($sid.Value,'S-1-5-18','S-1-5-32-544')) { throw 'Stage grants an unrelated principal access.' }
        }
        foreach ($path in @($stage,$evidenceRoot)) {
            $refused = $false
            try { [IO.Directory]::Move($path, $path + '.moved-by-fixture') } catch { $refused = $true }
            if (-not $refused) { throw 'A pinned namespace was movable during extraction.' }
        }
        $exit = Complete-Probe $pinned
        $pinned = $null
        if ($exit -ne 0 -or -not (Test-Path -LiteralPath (Join-Path $pinnedTarget 'zhvpn.exe'))) { throw 'Pinned stage failed complete publication.' }
        $rows.Add(@{ case='private owner DACL and parent-stage pins span extraction'; exit_code=$exit })
    } finally { if ($null -ne $pinned) { $pinned.WaitForExit(15000) | Out-Null; $pinned.Dispose() } }

    $untrusted = Join-Path $evidenceRoot 'untrusted-parent'
    New-Item -ItemType Directory -Path $untrusted | Out-Null
    $untrustedAcl = Get-Acl -LiteralPath $untrusted
    $untrustedAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-1-0'), 'Modify', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
    Set-Acl -LiteralPath $untrusted -AclObject $untrustedAcl
    $untrustedTarget = Join-Path $untrusted 'new-install'
    $exit = Complete-Probe (Start-Probe 'untrusted-parent' $untrustedTarget)
    if ($exit -eq 0 -or (Test-Path -LiteralPath $untrustedTarget) -or @(Get-ChildItem -LiteralPath $untrusted -Force).Count -ne 0) { throw 'An untrusted writable namespace was used for staging.' }
    $rows.Add(@{ case='untrusted writable parent refuses before staging'; exit_code=$exit })

    $junction = Join-Path $evidenceRoot 'junction-parent'
    New-Item -ItemType Junction -Path $junction -Target $evidenceRoot | Out-Null
    $junctionTarget = Join-Path $junction 'alias-install'
    $exit = Complete-Probe (Start-Probe 'junction-parent' $junctionTarget)
    if ($exit -eq 0 -or (Test-Path -LiteralPath (Join-Path $evidenceRoot 'alias-install'))) { throw 'A reparse namespace was used for staging.' }
    $rows.Add(@{ case='junction parent refuses before staging'; exit_code=$exit })

    # Render and compile the complete production Handlebars template using the
    # actual pinned Tauri CLI. These payloads are the owned synthetic child,
    # never a product GUI/CLI. The resulting installer is not executed.
    $triple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
    if ($triple -notlike '*-pc-windows-msvc') { throw 'Template syntax fixture requires an MSVC Windows host.' }
    $syntaxTarget = Join-Path $evidenceRoot 'syntax-target'
    $syntaxDebug = Join-Path $syntaxTarget 'debug'
    $syntaxSidecar = Join-Path $evidenceRoot 'syntax-sidecar'
    New-Item -ItemType Directory -Path $syntaxDebug,$syntaxSidecar | Out-Null
    Copy-Item -LiteralPath $childImage -Destination (Join-Path $syntaxDebug 'zhvpn-desktop.exe')
    Copy-Item -LiteralPath $childImage -Destination (Join-Path $syntaxSidecar "zhvpn-$triple.exe")
    $syntaxConfig = Join-Path $evidenceRoot 'syntax-config.json'
    @{ productName='zhvpn-synthetic-template-probe'; bundle=@{externalBin=@((Join-Path $syntaxSidecar 'zhvpn'));windows=@{webviewInstallMode=@{type='skip'}}}} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $syntaxConfig -Encoding utf8
    $hadTarget = Test-Path Env:CARGO_TARGET_DIR
    $previousTarget = $env:CARGO_TARGET_DIR
    $hadTauriConfig = Test-Path Env:TAURI_CONFIG
    $previousTauriConfig = $env:TAURI_CONFIG
    Push-Location (Join-Path $PSScriptRoot '..')
    try {
        $env:CARGO_TARGET_DIR = $syntaxTarget
        Remove-Item Env:TAURI_CONFIG -ErrorAction SilentlyContinue
        npm run tauri bundle -- --debug --bundles nsis --no-sign --ci --config $syntaxConfig
        if ($LASTEXITCODE -ne 0) { throw 'The actual Tauri full installer template failed to render or compile.' }
        $syntaxInstallers = @(Get-ChildItem -LiteralPath (Join-Path $syntaxDebug 'bundle/nsis') -Filter '*.exe' -File)
        if ($syntaxInstallers.Count -ne 1) { throw 'Full template did not produce exactly one owned syntax fixture.' }
        $rows.Add(@{ case='actual Tauri CLI renders and NSIS compiles full template'; executed=$false; payload='owned synthetic child'; installer_sha256=(Get-FileHash -LiteralPath $syntaxInstallers[0].FullName -Algorithm SHA256).Hash.ToLowerInvariant() })
    } finally {
        if ($hadTarget) { $env:CARGO_TARGET_DIR = $previousTarget } else { Remove-Item Env:CARGO_TARGET_DIR -ErrorAction SilentlyContinue }
        if ($hadTauriConfig) { $env:TAURI_CONFIG = $previousTauriConfig } else { Remove-Item Env:TAURI_CONFIG -ErrorAction SilentlyContinue }
        Pop-Location
    }
    if ($other.HasExited) { throw 'Unrelated owned child was stopped.' }
    $rows | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $evidenceRoot 'results.json') -Encoding utf8
    Write-Host "Fresh-install synthetic Windows checks passed: $evidenceRoot"
} finally {
    Stop-OwnedChild $current
    Stop-OwnedChild $other
}
