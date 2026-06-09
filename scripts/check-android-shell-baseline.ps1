param(
    [string]$ReverseService = "egress/reverse/service.d/99-dxreverse-egress.sh",
    [string]$Watchdog = "egress/android-control/watchdog.sh",
    [string]$AdbTcpService = "egress/android-control/service.d/97-dxadb-tcp-wg-only.sh"
)

$ErrorActionPreference = "Stop"

function Assert-Contains([string]$Path, [string]$Pattern, [string]$Message) {
    $text = Get-Content -Raw -Path $Path
    if ($text -notmatch $Pattern) {
        throw "$Path missing: $Message"
    }
}

function Assert-LiteralContains([string]$Path, [string]$Needle, [string]$Message) {
    $text = Get-Content -Raw -Path $Path
    if (-not $text.Contains($Needle)) {
        throw "$Path missing: $Message"
    }
}

function Assert-Balanced([string]$Path, [string]$Open, [string]$Close, [string]$Name) {
    $text = Get-Content -Raw -Path $Path
    $openCount = ([regex]::Matches($text, $Open)).Count
    $closeCount = ([regex]::Matches($text, $Close)).Count
    if ($openCount -ne $closeCount) {
        throw "${Path} unbalanced ${Name}: open=$openCount close=$closeCount"
    }
}

foreach ($path in @($ReverseService, $Watchdog, $AdbTcpService)) {
    if (-not (Test-Path $path)) {
        throw "missing script: $path"
    }
    Assert-Balanced $path "\bif\b" "\bfi\b" "if/fi"
    Assert-Balanced $path "\bcase\b" "\besac\b" "case/esac"
    Assert-Balanced $path "\bdo\b" "\bdone\b" "do/done"
}

Assert-Contains $ReverseService "svc wifi disable" "Wi-Fi disable baseline"
Assert-Contains $ReverseService "net\.core\.rmem_max" "receive buffer tuning"
Assert-Contains $ReverseService "99-dxandroid-egress\.sh" "legacy service disable"
Assert-LiteralContains $ReverseService 'pgrep -f "$pattern"' "legacy process stop"

Assert-Contains $Watchdog "ensure_network_baseline" "runtime network baseline"
Assert-Contains $Watchdog "svc wifi disable" "runtime Wi-Fi disable"
Assert-Contains $Watchdog "99-dxandroid-egress\.sh" "runtime legacy service disable"
Assert-Contains $Watchdog "99-dxreverse-egress" "reverse supervisor check"

Assert-Contains $AdbTcpService "PORT=.*5555" "TCP ADB port"
Assert-Contains $AdbTcpService "WG_IF=.*tun0" "WireGuard-only interface"
Assert-Contains $AdbTcpService "WG_CIDR=.*10\.66\.0\.0/24" "WireGuard CIDR allowlist"
Assert-Contains $AdbTcpService "iptables" "IPv4 firewall guard"
Assert-Contains $AdbTcpService "ip6tables" "IPv6 firewall guard"
Assert-Contains $AdbTcpService "DROP" "non-WireGuard ADB drop"

Write-Host "PASS android shell baseline checks"
