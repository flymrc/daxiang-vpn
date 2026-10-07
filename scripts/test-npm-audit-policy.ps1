# A real npm/registry regression: dev-only vulnerable metadata must stay blocked
# under inherited omit=dev. No packages, lifecycle scripts or product installs run.
param()
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'npm-audit-policy.ps1')
$taskFixture = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-npm-policy-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskFixture | Out-Null
$taskPrevious = [Environment]::GetEnvironmentVariable('NPM_CONFIG_OMIT','Process')
try {
    [IO.File]::WriteAllText((Join-Path $taskFixture 'package.json'), '{"name":"zhvpn-audit-negative-fixture","version":"1.0.0","private":true,"devDependencies":{"cookie":"0.6.0"}}')
    Push-Location $taskFixture
    try {
        & npm install --package-lock-only --ignore-scripts --include=dev --include=optional --include=peer --no-audit
        if ($LASTEXITCODE -ne 0) { throw 'Npm negative fixture metadata unavailable.' }
        $env:NPM_CONFIG_OMIT = 'dev'
        & npm audit --json 1> (Join-Path $taskFixture 'omitted.json')
        if ($LASTEXITCODE -ne 0) { throw 'Npm inherited omit control did not reproduce; inspect fixture/scanner behavior.' }
        $taskOmitted = Get-Content -LiteralPath (Join-Path $taskFixture 'omitted.json') -Raw | ConvertFrom-Json
        if ($taskOmitted.metadata.vulnerabilities.total -ne 0) { throw 'Npm inherited omit control inventory unexpectedly included dev.' }
    } finally { Pop-Location }
    $taskReport = Join-Path $taskFixture 'included.json'
    $taskBlocked = $false
    try { Invoke-ZhNpmAudit -ProjectDirectory $taskFixture -ReportPath $taskReport }
    catch { $taskBlocked = $true }
    $taskIncluded = Get-Content -LiteralPath $taskReport -Raw | ConvertFrom-Json
    if (-not $taskBlocked -or $taskIncluded.auditReportVersion -ne 2 -or $taskIncluded.metadata.vulnerabilities.total -lt 1 -or
        -not $taskIncluded.vulnerabilities.cookie) { throw 'Dev-only vulnerable package escaped the real audit helper.' }
    Write-Host "Real npm inherited omit negative passed; evidence: $taskFixture"
} finally {
    [Environment]::SetEnvironmentVariable('NPM_CONFIG_OMIT',$taskPrevious,'Process')
    # Retain the small fixture/reports as evidence; do not recursively delete it.
}
