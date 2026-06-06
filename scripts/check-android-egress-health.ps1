param(
    [string]$Hub = "root@36.50.84.68",
    [string]$Proxy = "http://10.66.0.101:1080",
    [string]$AndroidIP = "10.66.0.101",
    [int]$ExpectedRouteMtu = 1280,
    [int]$StaleHandshakeSeconds = 180,
    [int]$TimeoutSeconds = 10,
    [switch]$Benchmark
)

$ErrorActionPreference = "Stop"

$failed = $false

function Write-Check([string]$Level, [string]$Message) {
    Write-Host ("[{0}] {1}" -f $Level, $Message)
}

function Invoke-Hub([string]$Command) {
    return ssh $Hub $Command
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

$egressIP = Join-Output (Invoke-Hub "curl -sS -m '$TimeoutSeconds' -x '$Proxy' https://api.ipify.org || true")
if ($egressIP -match "^\d{1,3}(\.\d{1,3}){3}$") {
    Write-Check "PASS" ("proxy reachable, egress_ip={0}" -f $egressIP)
} elseif ($egressIP.Length -gt 0) {
    Write-Check "WARN" ("proxy returned unexpected egress_ip='{0}'" -f $egressIP)
} else {
    Write-Check "FAIL" "proxy did not return an egress IP"
    Set-Failed
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
    Write-Check "PASS" ("hub TCPMSS rule present: {0}" -f ($mssRules -replace "`r?`n", " | "))
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
