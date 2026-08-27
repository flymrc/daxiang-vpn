# 构建纵横 VPN Windows 客户端发布包。
#
# sing-box 以代码库形式编译进 zhvpn.exe（进程内运行），不再内嵌外部 exe。
# 体积优化：
#   -tags with_gvisor   编译 WireGuard 所需的 gVisor 用户态网络栈（必需）
#   -trimpath           去除编译路径信息
#   -ldflags "-s -w"    剥离符号表和调试信息（方案 1）
# 只注册用到的协议（mixed / http / wireguard …），让链接器死代码消除
# 丢掉 sing-box 自带的其余几十种协议，二进制约 17MB。

param(
    [Parameter(Mandatory = $true)]
    [string]$Version
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($Version) -or $Version -eq "dev") {
    throw "Version must be an explicit release version"
}

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$tags = "with_gvisor"
$ldflags = "-s -w -X zongheng-vpn/clients/cli/internal/buildinfo.Product=cli -X zongheng-vpn/clients/cli/internal/buildinfo.Version=$Version"
$hostGoOS = (go env GOOS).Trim()
$hostGoArch = (go env GOARCH).Trim()

$targets = @(
    @{ Arch = "amd64"; Dir = "windows-amd64" },
    @{ Arch = "arm64"; Dir = "windows-arm64" }
)

foreach ($t in $targets) {
    $outDir = Join-Path $repoRoot "dist\$($t.Dir)"
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    $out = Join-Path $outDir "zhvpn.exe"

    Write-Host "构建 $($t.Arch) -> $out"
    $env:GOOS = "windows"
    $env:GOARCH = $t.Arch
    go build -tags $tags -trimpath -ldflags $ldflags -o $out .
    if ($LASTEXITCODE -ne 0) { throw "构建 $($t.Arch) 失败" }

    if ($hostGoOS -eq "windows" -and $t.Arch -eq $hostGoArch) {
        $identity = (& $out version --json | ConvertFrom-Json)
        if ($identity.product -ne "cli" -or $identity.version -ne $Version -or $identity.protocol_version -ne 2) {
            throw "CLI identity mismatch: $($identity | ConvertTo-Json -Compress)"
        }
    }

    $sizeMB = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Host "  完成：$sizeMB MB"
}

Write-Host "全部构建完成。"
