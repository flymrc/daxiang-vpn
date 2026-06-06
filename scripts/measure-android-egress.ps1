param(
    [string]$Hub = "root@36.50.84.68",
    [string]$Proxy = "http://10.66.0.101:1080",
    [string]$Url = "https://speed.cloudflare.com/__down?bytes=20000000",
    [string]$FallbackUrl = "https://proof.ovh.net/files/10Mb.dat",
    [int]$Runs = 5,
    [int]$TimeoutSeconds = 25
)

$ErrorActionPreference = "Stop"

if ($Runs -lt 1) {
    throw "Runs must be at least 1."
}

$ip = ssh $Hub "curl -sS -m 10 -x '$Proxy' https://api.ipify.org || true"
Write-Host "egress_ip=$ip"

function Invoke-Benchmark([string]$TargetUrl, [string]$Label) {
    Write-Host "target=$Label url=$TargetUrl"
    $lines = @()
    for ($i = 1; $i -le $Runs; $i++) {
        $line = ssh $Hub "curl -sS -L --max-time '$TimeoutSeconds' -x '$Proxy' -o /dev/null -w 'run=$i code=%{http_code} bytes=%{size_download} bps=%{speed_download} seconds=%{time_total}' '$TargetUrl'; rc=`$?; echo ' exit='`$rc"
        $lines += $line
        Write-Host $line
    }
    return $lines
}

function Convert-Samples($Lines) {
    $samples = @()
    foreach ($line in $Lines) {
        if ($line -match "run=(\d+) code=(\d+) bytes=(\d+) bps=([0-9.]+) seconds=([0-9.]+) exit=(\d+)") {
            $samples += [pscustomobject]@{
                Run = [int]$matches[1]
                Code = [int]$matches[2]
                Bytes = [int64]$matches[3]
                Bps = [double]$matches[4]
                Mbps = [math]::Round(([double]$matches[4] * 8 / 1000000), 2)
                Seconds = [double]$matches[5]
                Exit = [int]$matches[6]
            }
        }
    }
    return $samples
}

$output = Invoke-Benchmark $Url "primary"
$samples = Convert-Samples $output

if ($samples.Count -eq 0) {
    throw "No benchmark samples were parsed."
}

$successful = $samples | Where-Object { $_.Exit -eq 0 -and $_.Code -eq 200 -and $_.Bytes -gt 0 }
if ($successful.Count -eq 0 -and $FallbackUrl -ne "" -and ($samples | Where-Object { $_.Code -eq 429 }).Count -gt 0) {
    Write-Host "primary target returned only rate-limited samples; retrying fallback"
    $output = Invoke-Benchmark $FallbackUrl "fallback"
    $samples = Convert-Samples $output
    $successful = $samples | Where-Object { $_.Exit -eq 0 -and $_.Code -eq 200 -and $_.Bytes -gt 0 }
}

if ($successful.Count -eq 0) {
    throw "No successful benchmark samples."
}

$avg = ($successful | Measure-Object -Property Mbps -Average).Average
$min = ($successful | Measure-Object -Property Mbps -Minimum).Minimum
$max = ($successful | Measure-Object -Property Mbps -Maximum).Maximum

Write-Host ("summary samples={0} avg_mbps={1:N2} min_mbps={2:N2} max_mbps={3:N2}" -f $successful.Count, $avg, $min, $max)
