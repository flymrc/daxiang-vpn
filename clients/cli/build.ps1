# Windows CLI entry point; shared builder owns gates and artifact identity.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$OutputDirectory,
    [string]$Version = 'dev',
    [switch]$Development
)
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot '../../scripts/build-cli.ps1') -Platform windows -OutputDirectory $OutputDirectory -Version $Version -Development:$Development
if ($LASTEXITCODE -ne 0) { throw 'CLI build failed.' }
