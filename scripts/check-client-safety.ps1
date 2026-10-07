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
    Invoke-Gate 'Trusted update metadata schema' 'go' @('run', './shared/updateverify/cmd/schemagen', '-check')
    Invoke-Gate 'Protected update policy and receipt schemas' 'go' @('run', './clients/cli/internal/updateclient/cmd/schemagen', '-check')
    Invoke-Gate 'Device start receipt schema and public receipt enums' 'go' @('run', './clients/cli/internal/deviceclient/cmd/schemagen', '-check')
    & (Join-Path $PSScriptRoot 'check-device-contract.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Device OpenAPI contract gate failed.' }
    & (Join-Path $PSScriptRoot 'check-admin-contract.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Admin OpenAPI/sqlc contract gate failed.' }
    Invoke-Gate 'Go tests (product build tags)' 'go' @('test', '-tags', 'with_gvisor', './...')
    Invoke-Gate 'Go vet' 'go' @('vet', '-tags', 'with_gvisor', './...')
    Invoke-Gate 'Client and reverse race tests' 'go' @('test', '-race', '-tags', 'with_gvisor', './clients/cli/...', './shared/...', './egress/reverse')
    Invoke-Gate 'Device authority/API race tests' 'go' @('test', '-race', './hub/internal/deviceauth', './hub/internal/deviceapi')
    Invoke-Gate 'Hub compatibility and admin race tests' 'go' @('test', '-race', './hub/internal/auth', './hub/admin/...', './hub/internal/httpboundary', './hub/internal/processbudget')
    Invoke-Gate 'Actual CLI and Hub TLS interoperability' 'go' @('test', '-race', '-tags', 'integration', './hub/internal/deviceapi', '-run', 'TestRealCLIAndHubTLSRecoverLostMutationResponses', '-count=1')
    Invoke-Gate 'Actual WireGuard traffic, startup TLS gate, protected peer and CLI revocation' 'go' @('test', '-race', '-tags', 'integration,with_gvisor', './hub/internal/deviceapi', '-run', '^Test(Real(WireGuard|CLIProxy)|.*Service.*)', '-count=1')
    Invoke-Gate 'Actual provisional inventory, SQLite faults and observer restarts' 'go' @('test', '-race', '-tags', 'integration', './hub/admin/internal/api', '-run', '^TestCampaignIndependent', '-count=1')
    Invoke-Gate 'Actual offline update CLI and cross-process watermarks' 'go' @('test', '-race', '-tags', 'integration', './clients/cli/internal/updateclient', '-run', '^TestCLIUpdate', '-count=1')
    Invoke-Gate 'Python SDK consumers' 'python' @('-m', 'unittest', 'discover', '-s', 'sdk/python/tests', '-v')
    Invoke-Gate 'Security evidence parser' 'python' @('-m', 'unittest', 'discover', '-s', 'scripts/tests', '-v')
    Invoke-Gate 'Real npm audit refuses inherited dev omission' 'pwsh' @('-NoProfile', '-File', 'scripts/test-npm-audit-policy.ps1')
    Invoke-Gate 'CLI builders refuse unsafe release and source changes' 'pwsh' @('-NoProfile', '-File', 'scripts/test-cli-build.ps1')
    Invoke-Gate 'Inner desktop builder refuses security failure' 'pwsh' @('-NoProfile', '-File', 'clients/desktop-gui/scripts/check-build-failure.ps1')
    if ($IsWindows) {
        Invoke-Gate 'SDK builder identity/install failures preserve refusal' 'pwsh' @('-NoProfile', '-File', 'scripts/test-sdk-build.ps1')
        Invoke-Gate 'Owned fresh-install guard and full template syntax' 'pwsh' @('-NoProfile', '-File', 'clients/desktop-gui/scripts/check-upgrade-guard.ps1')
    }
    else { Write-Host 'Native NSIS fixture checks require Windows; installer release acceptance is unavailable on this host.' }

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
            Invoke-Gate 'GUI Rust static checks' 'cargo' @('clippy', '--locked', '--manifest-path', 'src-tauri/Cargo.toml', '--lib', '--', '-D', 'warnings')
        }
        finally {
            if ($hadTauriConfig) { $env:TAURI_CONFIG = $previousTauriConfig }
            else { Remove-Item Env:TAURI_CONFIG -ErrorAction SilentlyContinue }
        }
    }
    finally { Pop-Location }
    Push-Location 'hub/admin/web'
    try {
        if (-not (Test-Path -LiteralPath 'node_modules')) { throw 'Admin dependencies missing; run npm ci in hub/admin/web.' }
        Invoke-Gate 'Admin types and Svelte' 'npm' @('run', 'check')
        Invoke-Gate 'Admin runtime contract consumers' 'npm' @('test')
    }
    finally { Pop-Location }
    Write-Host 'Client safety gates passed. Release signing and production acceptance remain separate.'
}
finally { Pop-Location }
