param(
    [ValidateSet("x64", "amd64", "arm64", "host")]
    [string]$Target = "host",
    [switch]$Install,
    [switch]$WithRequests,
    [switch]$Wheel,
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

$sdkDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $sdkDir "..\..")
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
        $arch = (go env GOARCH).Trim()
        if ($arch -eq "amd64") { return "amd64" }
        if ($arch -eq "arm64") { return "arm64" }
        throw "Unsupported host GOARCH for Windows CLI bundle: $arch"
    }
    if ($Name -eq "x64") { return "amd64" }
    return $Name
}

$goarch = Resolve-Target $Target
$out = Join-Path $binDir "zhvpn.exe"
$hostGoOS = (go env GOOS).Trim()
$hostGoArch = (go env GOARCH).Trim()

Push-Location $repo
try {
    $env:GOOS = "windows"
    $env:GOARCH = $goarch
    if ($goarch -eq "amd64") {
        $env:GOAMD64 = "v1"
    }
    $ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=python-sdk -X zongheng-vpn/clients/cli/internal/buildinfo.Version=$Version"
    go build -tags with_gvisor -trimpath -ldflags $ldflags -o $out .\clients\cli
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE. If $out is locked, disconnect the SDK-started VPN engine and close Python processes using zongheng_vpn, then retry."
    }
} finally {
    Pop-Location
}

Write-Host "Bundled CLI: $out"
if ($hostGoOS -eq "windows" -and $goarch -eq $hostGoArch) {
    $identity = (& $out version --json | ConvertFrom-Json)
    if ($identity.product -ne "python-sdk" -or $identity.version -ne $Version -or $identity.protocol_version -ne 2) {
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
}

if ($Wheel) {
    $wheelDir = Join-Path $sdkDir "dist"
    New-Item -ItemType Directory -Force -Path $wheelDir | Out-Null
    python -m pip wheel $sdkDir --no-deps -w $wheelDir
}
