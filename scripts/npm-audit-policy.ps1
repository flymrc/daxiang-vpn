function Invoke-ZhNpmAudit {
    param([Parameter(Mandatory=$true)][string]$ProjectDirectory,
          [Parameter(Mandatory=$true)][string]$ReportPath)
    if (Test-Path -LiteralPath $ReportPath) { throw 'Npm evidence must use a fresh file.' }
    Push-Location $ProjectDirectory
    try {
        # Explicit include wins over user/npmrc/env omit and production settings.
        & npm audit --include=dev --include=optional --include=peer --json 1> $ReportPath
        if ($LASTEXITCODE -ne 0) { throw 'Npm dependency audit failed; raw report retained.' }
        & python (Join-Path $PSScriptRoot 'npm-security-report.py') $ReportPath
        if ($LASTEXITCODE -ne 0) { throw 'Npm audit evidence is incomplete or inconsistent.' }
    } finally { Pop-Location }
}
