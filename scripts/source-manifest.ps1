# Shared by local builders. Hash the complete non-ignored source inventory,
# including dirty additions/deletions; never echo source contents.
function Get-ZhSourceManifest([string]$Repository) {
    $root = (Resolve-Path -LiteralPath $Repository).Path
    $listed = @(& git -C $root ls-files --cached --others --exclude-standard -z)
    if ($LASTEXITCODE -ne 0) { throw 'Source inventory unavailable.' }
    $paths = ([string]::Join("`n", $listed)).Split([char]0, [StringSplitOptions]::RemoveEmptyEntries) | Sort-Object -Unique
    foreach ($relative in $paths) {
        $path = [IO.Path]::GetFullPath((Join-Path $root $relative))
        if (-not $path.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Source inventory escaped its repository.' }
        [pscustomobject]@{
            path=$relative.Replace('\','/')
            sha256=$(if (Test-Path -LiteralPath $path -PathType Leaf) { (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() } else { $null })
        }
    }
}

function Assert-ZhSourceManifest([string]$Repository, [object[]]$Expected) {
    $actual = @(Get-ZhSourceManifest $Repository)
    if (($actual | ConvertTo-Json -Depth 4 -Compress) -cne ($Expected | ConvertTo-Json -Depth 4 -Compress)) { throw 'Source changed during build; refuse a success manifest.' }
}
