[CmdletBinding()]
param([Parameter(Mandatory)][string]$GuiPath, [Parameter(Mandatory)][string]$CliPath, [Parameter(Mandatory)][string]$Output)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$env:PSModulePath = Join-Path $PSHOME 'Modules'
$gui = (Get-FileHash -LiteralPath $GuiPath -Algorithm SHA256).Hash.ToLowerInvariant()
$cli = (Get-FileHash -LiteralPath $CliPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($gui -notmatch '^[a-f0-9]{64}$' -or $cli -notmatch '^[a-f0-9]{64}$') { throw 'Missing build payload hash.' }
@("!define ZHVPN_GUI_SHA256 `"$gui`"", "!define ZHVPN_CLI_SHA256 `"$cli`"") | Set-Content -LiteralPath $Output -Encoding ascii
