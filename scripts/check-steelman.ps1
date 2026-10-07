[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$EvidenceDirectory)
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'check-client-safety.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Behavior/contract gate failed.' }
& (Join-Path $PSScriptRoot 'check-security.ps1') -EvidenceDirectory $EvidenceDirectory
if ($LASTEXITCODE -ne 0) { throw 'Security gate failed.' }
Write-Host 'Implemented local gates passed. This is not Mac/phone acceptance, a signed release, or production migration approval.'
