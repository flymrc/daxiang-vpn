param(
    [string]$Hub = "root@36.50.84.68",
    [string]$HubIdentityFile = "",
    [string]$Proxy = "http://10.66.0.1:18081",
    [string]$AndroidIP = "10.66.0.101",
    # 主路径探针:必须返回 IPv6(生产 address_family: ipv6)。
    [string[]]$EgressIPv6Urls = @(
        "https://api6.ipify.org",
        "https://api64.ipify.org"
    ),
    # v4 兜底探针:手机 v4 经乐天 F5,时好时坏,失败仅 WARN。
    [string]$EgressIPv4Url = "https://api.ipify.org",
    [string]$HubPublicIP = "36.50.84.68",
    [int]$ExpectedRouteMtu = 1120,
    [int]$ExpectedMss = 0,
    [int]$StaleHandshakeSeconds = 180,
    [int]$TimeoutSeconds = 10,
    [switch]$Benchmark
)

$ErrorActionPreference = "Stop"

$failed = $false
if ($ExpectedMss -eq 0) {
    $ExpectedMss = $ExpectedRouteMtu - 40
}

function Write-Check([string]$Level, [string]$Message) {
    Write-Host ("[{0}] {1}" -f $Level, $Message)
}

function Invoke-Hub([string]$Command) {
    $args = @()
    if ($HubIdentityFile.Length -gt 0) {
        $args += @("-i", $HubIdentityFile)
    }
    $args += @($Hub, $Command)
    return ssh @args
}

function Join-Output($Output) {
    if ($null -eq $Output) {
        return ""
    }
    return (($Output | Out-String).Trim())
}

function Set-Failed {
    $script:failed = $true
}

Write-Host ("hub={0}" -f $Hub)
Write-Host ("android_ip={0}" -f $AndroidIP)
Write-Host ("proxy={0}" -f $Proxy)

# 主路径:v6。拿不到 v6 出口 = FAIL(api64 双栈兜底,但只认 v6 形态结果)。
$egressIPv6 = ""
$egressSource = ""
$egressErrors = @()
foreach ($url in $EgressIPv6Urls) {
    $body = Join-Output (Invoke-Hub "curl -sS -m '$TimeoutSeconds' -x '$Proxy' '$url' 2>/tmp/zhreverse-health-curl.err || true; cat /tmp/zhreverse-health-curl.err >&2; rm -f /tmp/zhreverse-health-curl.err")
    if ($body -match "^[0-9a-fA-F:]{3,}$" -and $body.Contains(":")) {
        $egressIPv6 = $body
        $egressSource = $url
        break
    }
    if ($body.Length -gt 0) {
        $egressErrors += ("{0}: {1}" -f $url, ($body -replace "`r?`n", " | "))
    } else {
        $egressErrors += ("{0}: empty response" -f $url)
    }
}
if ($egressIPv6.Length -gt 0) {
    if ($egressIPv6 -like "240b:*") {
        Write-Check "PASS" ("v6 egress ok (Rakuten), egress_ip={0}, source={1}" -f $egressIPv6, $egressSource)
    } else {
        Write-Check "PASS" ("v6 egress ok (non-Rakuten prefix, check SIM/carrier), egress_ip={0}, source={1}" -f $egressIPv6, $egressSource)
    }
} else {
    Write-Check "FAIL" ("v6 egress (main path) unavailable; tried {0}" -f ($EgressIPv6Urls -join ", "))
    foreach ($err in $egressErrors) {
        Write-Check "WARN" $err
    }
    Set-Failed
}

# v4 兜底:失败仅 WARN;等于 Hub IP 则是 hub-fallback 回归,FAIL。
$v4Body = Join-Output (Invoke-Hub "curl -sS -m '$TimeoutSeconds' -x '$Proxy' '$EgressIPv4Url' 2>/dev/null || true")
if ($v4Body -match "^\d{1,3}(\.\d{1,3}){3}$") {
    if ($v4Body -eq $HubPublicIP) {
        Write-Check "FAIL" ("v4 egress is Hub VPS {0}: hub-fallback regression" -f $HubPublicIP)
        Set-Failed
    } else {
        Write-Check "PASS" ("v4 egress ok (phone v4, flaky by nature), egress_ip={0}" -f $v4Body)
    }
} else {
    Write-Check "WARN" ("v4 egress unavailable (F5 v4 bad window; v4-only sites degraded): {0}" -f $v4Body)
}

$route = Join-Output (Invoke-Hub "ip route show '$AndroidIP/32' || true")
if ($route -match "mtu\s+$ExpectedRouteMtu\b") {
    Write-Check "PASS" ("hub route mtu ok: {0}" -f $route)
} elseif ($route.Length -gt 0) {
    Write-Check "FAIL" ("hub route mtu unexpected: {0}" -f $route)
    Set-Failed
} else {
    Write-Check "FAIL" ("hub route missing for {0}/32" -f $AndroidIP)
    Set-Failed
}

$mssRules = Join-Output (Invoke-Hub "iptables -t mangle -S FORWARD 2>/dev/null | grep -- 'TCPMSS' || true")
if ($mssRules.Length -gt 0) {
    if ($mssRules -match "--set-mss\s+$ExpectedMss\b") {
        Write-Check "PASS" ("hub TCPMSS rule ok: {0}" -f ($mssRules -replace "`r?`n", " | "))
    } else {
        Write-Check "FAIL" ("hub TCPMSS rule unexpected: {0}" -f ($mssRules -replace "`r?`n", " | "))
        Set-Failed
    }
} else {
    Write-Check "WARN" "no hub TCPMSS rule found"
}

$peerKey = ""
$allowedIps = Invoke-Hub "wg show wg0 allowed-ips 2>/dev/null || true"
foreach ($line in $allowedIps) {
    if ($line -match "^\s*(\S+)\s+$([regex]::Escape($AndroidIP))/32\s*$") {
        $peerKey = $matches[1]
        break
    }
}

if ($peerKey.Length -gt 0) {
    $handshake = [int64]0
    $latestHandshakes = Invoke-Hub "wg show wg0 latest-handshakes 2>/dev/null || true"
    foreach ($line in $latestHandshakes) {
        if ($line -match "^\s*$([regex]::Escape($peerKey))\s+(\d+)\s*$") {
            $handshake = [int64]$matches[1]
            break
        }
    }
    $now = [int64](Join-Output (Invoke-Hub "date +%s"))
    if ($handshake -gt 0) {
        $age = $now - $handshake
        if ($age -le $StaleHandshakeSeconds) {
            Write-Check "PASS" ("wireguard handshake fresh, age_seconds={0}" -f $age)
        } else {
            Write-Check "WARN" ("wireguard handshake stale, age_seconds={0}" -f $age)
        }
    } else {
        Write-Check "FAIL" "wireguard handshake has never completed"
        Set-Failed
    }
} else {
    Write-Check "WARN" "could not map Android peer in wg show output"
}

if ($Benchmark) {
    Write-Host ""
    Write-Host "benchmark=enabled"
    & (Join-Path $PSScriptRoot "measure-android-egress.ps1") -Hub $Hub -Proxy $Proxy -Runs 2 -TimeoutSeconds 25
}

if ($failed) {
    exit 1
}

exit 0
