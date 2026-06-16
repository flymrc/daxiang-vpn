param(
    [ValidateSet("x64", "amd64", "arm64", "host")]
    [string]$Target = "host",
    [switch]$Install,
    [switch]$WithRequests,
    [switch]$Wheel,
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"

$sdkDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repo = Resolve-Path (Join-Path $sdkDir "..\..")
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

Push-Location $repo
try {
    $env:GOOS = "windows"
    $env:GOARCH = $goarch
    if ($goarch -eq "amd64") {
        $env:GOAMD64 = "v1"
    }
    $ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/app.Version=$Version"
    go build -tags with_gvisor -trimpath -ldflags $ldflags -o $out .\clients\cli
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE. If $out is locked, disconnect the SDK-started VPN engine and close Python processes using zongheng_vpn, then retry."
    }
} finally {
    Pop-Location
}

Write-Host "Bundled CLI: $out"
& $out version --json

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
