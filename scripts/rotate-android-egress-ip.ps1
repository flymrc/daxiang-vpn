# 从 Windows 一键切换安卓出口的公网 IP。
#
# 实际换 IP 的逻辑在手机上的 /data/adb/zhandroid/rotate-ip.sh(飞行模式重注册);
# 本脚本只是经 SSH 远程触发它,并在前后打印出口公网 IP 方便对比。
#
# 前提:本机已在 WireGuard 隧道内(能直连 10.66.0.101)。
#
# 用法:
#   .\scripts\rotate-android-egress-ip.ps1
#   .\scripts\rotate-android-egress-ip.ps1 -DownSeconds 12 -WaitSeconds 40

param(
    [int]$DownSeconds = 8,        # 飞行模式保持秒数(越长换 IP 概率越高)
    [int]$WaitSeconds = 30,       # 触发后等待无线电重注册 + 隧道恢复的秒数
    [string]$Phone = "10.66.0.101",
    [int]$Port = 2022,
    [string]$Key = "$HOME\.ssh\zhandroid_control",
    [string]$Proxy = "http://10.66.0.1:18081"
)

$sshArgs = @(
    "-i", $Key, "-p", $Port,
    "-o", "StrictHostKeyChecking=no",
    "-o", "UserKnownHostsFile=NUL",
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=8",
    "root@$Phone"
)

function Get-EgressIP {
    $ip = curl.exe -s -m 20 -x $Proxy https://api.ipify.org 2>$null
    if ([string]::IsNullOrWhiteSpace($ip)) { return "(暂不可达)" }
    return $ip.Trim()
}

Write-Host ("换 IP 前出口: {0}" -f (Get-EgressIP))

Write-Host ("触发 rotate-ip(断网 {0}s,脱离会话执行)..." -f $DownSeconds)
ssh @sshArgs "sh /data/adb/zhandroid/rotate-ip.sh $DownSeconds" 2>&1 | Out-Host

Write-Host ("等待 {0}s 让无线电重注册 + 隧道恢复..." -f $WaitSeconds)
Start-Sleep -Seconds $WaitSeconds

$after = Get-EgressIP
Write-Host ("换 IP 后出口: {0}" -f $after)
if ($after -eq "(暂不可达)") {
    Write-Host "提示:可能还没恢复,过几秒再 curl.exe -x $Proxy https://api.ipify.org 看看;或加大 -WaitSeconds。"
}
