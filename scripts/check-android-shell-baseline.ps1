param(
    [string]$ReverseService = "egress/reverse/service.d/99-dxreverse-egress.sh",
    [string]$Watchdog = "egress/android-control/watchdog.sh"
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

foreach ($path in @($ReverseService, $Watchdog)) {
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

Write-Host "PASS android shell baseline checks"
