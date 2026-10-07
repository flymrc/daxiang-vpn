$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$env:PSModulePath = Join-Path $PSHOME 'Modules'
try {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $trusted = @($sid, 'S-1-5-18', 'S-1-5-32-544')
    $path = [IO.Path]::GetFullPath($env:ZHVPN_INSTALL_PARENT)
    if ($path.StartsWith('\\') -or ([IO.DriveInfo]::new([IO.Path]::GetPathRoot($path))).DriveType -ne 'Fixed') { throw 'Unsupported volume' }
    $directory = [IO.DirectoryInfo]::new($path)
    $first = $true
    while ($null -ne $directory) {
        if (-not $directory.Exists -or ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe namespace' }
        $acl = Get-Acl -LiteralPath $directory.FullName
        $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
        if ($first -and $owner -ne $sid) { throw 'Unexpected parent owner' }
        # Existing ancestors may grant CreateDirectories without granting the
        # right to replace an existing child. The immediate parent must allow
        # no untrusted mutation at all; all ancestors forbid namespace/DACL
        # mutation. Deny entries never make an untrusted Allow acceptable.
        $mask = if ($first) { 0xD0156 } else { 0xD0040 }
        foreach ($ace in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
            if ($ace.AccessControlType -eq 'Allow' -and ($ace.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0 -and ($ace.FileSystemRights.value__ -band $mask) -ne 0 -and $ace.IdentityReference.Value -notin $trusted) { throw 'Untrusted writable namespace' }
        }
        $first = $false
        $directory = $directory.Parent
    }
    [Console]::Write($sid)
    exit 0
} catch { exit 12 }
