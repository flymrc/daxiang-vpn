param(
    [string]$Hub = "root@36.50.84.68",
    [string]$HubIdentityFile = "",
    [string]$Proxy = "http://10.66.0.1:18081",
    [string[]]$Urls = @(
        "https://api64.ipify.org",
        "https://speed.cloudflare.com/__down?bytes=1024",
        "https://www.cloudflare.com/cdn-cgi/trace"
    ),
    [int]$Runs = 50,
    [int]$TimeoutSeconds = 15,
    [int]$PauseMs = 250,
    [switch]$Striped
)

$ErrorActionPreference = "Stop"

if ($Runs -lt 1) {
    throw "Runs must be at least 1."
}
if ($TimeoutSeconds -lt 1) {
    throw "TimeoutSeconds must be at least 1."
}
if ($Urls.Count -lt 1) {
    throw "At least one URL is required."
}

function Invoke-Hub([string]$Command) {
    $args = @()
    if ($HubIdentityFile.Length -gt 0) {
        $args += @("-i", $HubIdentityFile)
    }
    $args += @($Hub, $Command)
    return ssh @args
}

function Get-Percentile([double[]]$Values, [double]$Percentile) {
    if ($Values.Count -eq 0) {
        return $null
    }
    $sorted = @($Values | Sort-Object)
    $index = [math]::Ceiling($sorted.Count * $Percentile) - 1
    if ($index -lt 0) {
        $index = 0
    }
    if ($index -ge $sorted.Count) {
        $index = $sorted.Count - 1
    }
    return [math]::Round($sorted[$index] * 1000, 2)
}

function Convert-Samples($Lines) {
    $samples = @()
    foreach ($line in $Lines) {
        if ($line -match "url_index=(\d+) run=(\d+) code=(\d+) bytes=(\d+) connect=([0-9.]+) appconnect=([0-9.]+) starttransfer=([0-9.]+) total=([0-9.]+) exit=(\d+)") {
            $samples += [pscustomobject]@{
                UrlIndex = [int]$matches[1]
                Run = [int]$matches[2]
                Code = [int]$matches[3]
                Bytes = [int64]$matches[4]
                Connect = [double]$matches[5]
                AppConnect = [double]$matches[6]
                StartTransfer = [double]$matches[7]
                Total = [double]$matches[8]
                Exit = [int]$matches[9]
            }
        }
    }
    return $samples
}

function Write-LatencySummary([string]$Label, $Samples) {
    $ok = @($Samples | Where-Object { $_.Exit -eq 0 -and $_.Code -ge 200 -and $_.Code -lt 400 })
    $failed = $Samples.Count - $ok.Count
    Write-Host ("summary target={0} samples={1} ok={2} failed={3}" -f $Label, $Samples.Count, $ok.Count, $failed)
    if ($ok.Count -eq 0) {
        return
    }

    $appconnect = [double[]]@($ok | ForEach-Object { $_.AppConnect })
    $starttransfer = [double[]]@($ok | ForEach-Object { $_.StartTransfer })
    $total = [double[]]@($ok | ForEach-Object { $_.Total })

    Write-Host ("  appconnect_ms   p50={0} p95={1} p99={2} max={3}" -f `
        (Get-Percentile $appconnect 0.50), `
        (Get-Percentile $appconnect 0.95), `
        (Get-Percentile $appconnect 0.99), `
        ([math]::Round((($appconnect | Measure-Object -Maximum).Maximum) * 1000, 2)))
    Write-Host ("  starttransfer_ms p50={0} p95={1} p99={2} max={3}" -f `
        (Get-Percentile $starttransfer 0.50), `
        (Get-Percentile $starttransfer 0.95), `
        (Get-Percentile $starttransfer 0.99), `
        ([math]::Round((($starttransfer | Measure-Object -Maximum).Maximum) * 1000, 2)))
    Write-Host ("  total_ms         p50={0} p95={1} p99={2} max={3}" -f `
        (Get-Percentile $total 0.50), `
        (Get-Percentile $total 0.95), `
        (Get-Percentile $total 0.99), `
        ([math]::Round((($total | Measure-Object -Maximum).Maximum) * 1000, 2)))
}

$mode = "normal"
$proxyHeader = ""
if ($Striped) {
    $mode = "striped"
    $proxyHeader = "--proxy-header 'X-ZH-Striped-Streams: 2'"
}

Write-Host "hub=$Hub"
Write-Host "proxy=$Proxy"
Write-Host "mode=$mode"
Write-Host "runs_per_target=$Runs timeout_seconds=$TimeoutSeconds pause_ms=$PauseMs"

$allLines = @()
for ($urlIndex = 0; $urlIndex -lt $Urls.Count; $urlIndex++) {
    $targetUrl = $Urls[$urlIndex]
    Write-Host "target[$urlIndex]=$targetUrl"
    for ($i = 1; $i -le $Runs; $i++) {
        $cmd = "curl -sS -L --max-time '$TimeoutSeconds' -x '$Proxy' $proxyHeader -o /dev/null -w 'url_index=$urlIndex run=$i code=%{http_code} bytes=%{size_download} connect=%{time_connect} appconnect=%{time_appconnect} starttransfer=%{time_starttransfer} total=%{time_total}' '$targetUrl'; rc=`$?; echo ' exit='`$rc"
        $line = Invoke-Hub $cmd
        $allLines += $line
        Write-Host $line
        if ($PauseMs -gt 0) {
            Start-Sleep -Milliseconds $PauseMs
        }
    }
}

$samples = Convert-Samples $allLines
if ($samples.Count -eq 0) {
    throw "No latency samples were parsed."
}

for ($urlIndex = 0; $urlIndex -lt $Urls.Count; $urlIndex++) {
    $targetSamples = @($samples | Where-Object { $_.UrlIndex -eq $urlIndex })
    Write-LatencySummary $Urls[$urlIndex] $targetSamples
}

$healthUrl = $Proxy -replace "^http://", "http://"
$healthUrl = $healthUrl.TrimEnd("/") + "/debug/session-health"
$healthRaw = (Invoke-Hub "curl -sS -m 10 '$healthUrl' || true") -join "`n"
Write-Host "session_health_url=$healthUrl"
try {
    $health = $healthRaw | ConvertFrom-Json
    Write-Host ("hub_sessions={0} active_proxy={1} active_proxy_peak={2}" -f $health.session_count, $health.active_proxy_connections, $health.active_proxy_connections_peak)
    if ($health.proxy_metrics) {
        Write-Host ("hub_proxy_metrics samples={0} total={1} successes={2} failures={3}" -f `
            $health.proxy_metrics.sample_count, `
            $health.proxy_metrics.total_connects, `
            $health.proxy_metrics.successes, `
            $health.proxy_metrics.failures)
        Write-Host ("hub_first_byte_ms p50={0} p95={1} p99={2} max={3}" -f `
            $health.proxy_metrics.first_byte_latency_ms.p50, `
            $health.proxy_metrics.first_byte_latency_ms.p95, `
            $health.proxy_metrics.first_byte_latency_ms.p99, `
            $health.proxy_metrics.first_byte_latency_ms.max)
    }
} catch {
    Write-Host "session_health_raw=$healthRaw"
}
