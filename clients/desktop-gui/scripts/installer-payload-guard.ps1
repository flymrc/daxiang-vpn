$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$env:PSModulePath = Join-Path $PSHOME 'Modules'
try {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $path = [IO.Path]::GetFullPath($env:ZHVPN_STAGE_DIR)
    $directory = Get-Item -LiteralPath $path -Force
    if (-not $directory.PSIsContainer -or ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe stage' }
    $acl = Get-Acl -LiteralPath $path
    if (-not $acl.AreAccessRulesProtected -or $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid) { throw 'Stage is not private' }
    foreach ($ace in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        if ($ace.AccessControlType -eq 'Allow' -and $ace.IdentityReference.Value -notin @($sid, 'S-1-5-18', 'S-1-5-32-544')) { throw 'Stage is not private' }
    }
    foreach ($pair in @(@('zhvpn-desktop.exe', $env:ZHVPN_GUI_SHA256), @('zhvpn.exe', $env:ZHVPN_CLI_SHA256))) {
        if ($pair[1] -notmatch '^[a-fA-F0-9]{64}$') { throw 'Missing build hash' }
        $file = Get-Item -LiteralPath (Join-Path $path $pair[0]) -Force
        if ($file.PSIsContainer -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe image' }
        if ((Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash -ne $pair[1]) { throw 'Image hash mismatch' }
    }
    exit 0
} catch { exit 12 }
