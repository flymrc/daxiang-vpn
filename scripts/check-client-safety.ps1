# Local gates for implemented Steelman client/authorization slices. This is not the final
# release/security/signing gate. Install GUI dependencies with npm ci first.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

function Invoke-Gate {
    param([string]$Name, [string]$Executable, [string[]]$Arguments)
    Write-Host "[$Name]"
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed (exit $LASTEXITCODE); remaining gates were not run."
    }
}

Push-Location $repoRoot
try {
    Invoke-Gate 'Generated CLI contracts' 'go' @('run', './shared/contracts/cmd/contractgen', '-check')
    Invoke-Gate 'Go tests (product build tags)' 'go' @('test', '-tags', 'with_gvisor', './...')
    Invoke-Gate 'Go vet' 'go' @('vet', '-tags', 'with_gvisor', './...')
    Invoke-Gate 'Client and reverse race tests' 'go' @('test', '-race', '-tags', 'with_gvisor', './clients/cli/...', './shared/...', './egress/reverse')
    Invoke-Gate 'Offline authorization race tests' 'go' @('test', '-race', './hub/internal/deviceauth')
    Invoke-Gate 'Python SDK consumers' 'python' @('-m', 'unittest', 'discover', '-s', 'sdk/python/tests', '-v')

    Push-Location 'clients/desktop-gui'
    try {
        if (-not (Test-Path -LiteralPath 'node_modules')) {
            throw 'GUI dependencies missing; run npm ci in clients/desktop-gui first.'
        }
        Invoke-Gate 'GUI types and Svelte' 'npm' @('run', 'check')
        # Library tests exercise the real Windows adapters against synthetic
        # registry keys. They do not require a packaged CLI sidecar, and must
        # not be presented as a signed installer/release acceptance.
        $hadTauriConfig = Test-Path Env:TAURI_CONFIG
        $previousTauriConfig = $env:TAURI_CONFIG
        try {
            $env:TAURI_CONFIG = '{"bundle":{"externalBin":[]}}'
            Invoke-Gate 'GUI Rust behavior (library only)' 'cargo' @('test', '--locked', '--manifest-path', 'src-tauri/Cargo.toml', '--lib')
        }
        finally {
            if ($hadTauriConfig) { $env:TAURI_CONFIG = $previousTauriConfig }
            else { Remove-Item Env:TAURI_CONFIG -ErrorAction SilentlyContinue }
        }
    }
    finally { Pop-Location }
    Write-Host 'Client safety gates passed. Release signing and production acceptance remain separate.'
}
finally { Pop-Location }
