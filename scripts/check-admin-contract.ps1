param()
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$adminRoot = Join-Path $repoRoot 'hub/admin'
$taskTemp = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-admin-contract-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskTemp | Out-Null
function Assert-GeneratedFile([string]$Actual, [string]$Expected) {
    if (-not (Test-Path -LiteralPath $Expected -PathType Leaf) -or
        (Get-FileHash -Algorithm SHA256 -LiteralPath $Actual).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $Expected).Hash) {
        throw 'Admin generated contracts drifted; regenerate from canonical OpenAPI/sqlc sources.'
    }
}
try {
    $taskGo = Join-Path $taskTemp 'openapi_types.go'
    $taskSource = Join-Path $adminRoot 'internal/spec/openapi.yml'
    $taskOapi = Join-Path $taskTemp 'oapi-codegen.yaml'
    $taskCanonicalConfig = [IO.File]::ReadAllText((Join-Path $adminRoot 'internal/spec/oapi-codegen.yaml'))
    $taskOutputPattern = '(?m)^output:[^\r\n]*$'
    if ([regex]::Matches($taskCanonicalConfig, $taskOutputPattern).Count -ne 1) { throw 'Admin oapi config must declare exactly one root output; no fallback generation is permitted.' }
    $taskOutputLine = 'output: ' + ($taskGo | ConvertTo-Json -Compress)
    $taskRelocatedConfig = [regex]::Replace($taskCanonicalConfig, $taskOutputPattern, [Text.RegularExpressions.MatchEvaluator]{ param($match) $taskOutputLine })
    [IO.File]::WriteAllText($taskOapi, $taskRelocatedConfig)
    & go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 --config $taskOapi $taskSource
    if ($LASTEXITCODE -ne 0) { throw 'Admin OpenAPI Go generation failed.' }
    Assert-GeneratedFile $taskGo (Join-Path $adminRoot 'internal/spec/generated/openapi_types.go')
    $taskDb = Join-Path $taskTemp 'generated'
    $taskConfig = Join-Path $taskTemp 'sqlc.yaml'
    Copy-Item -LiteralPath (Join-Path $adminRoot 'internal/db/schema.sql') -Destination (Join-Path $taskTemp 'schema.sql')
    Copy-Item -LiteralPath (Join-Path $adminRoot 'internal/db/queries.sql') -Destination (Join-Path $taskTemp 'queries.sql')
    Copy-Item -LiteralPath (Join-Path $adminRoot 'internal/db/sqlc.yaml') -Destination $taskConfig
    & go run (Join-Path $adminRoot 'cmd/contract-config-guard') $taskConfig
    if ($LASTEXITCODE -ne 0) { throw 'Admin sqlc configuration refused before generation.' }
    & go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0 generate -f $taskConfig
    if ($LASTEXITCODE -ne 0) { throw 'Admin sqlc generation failed.' }
    $taskActual = @(Get-ChildItem -LiteralPath $taskDb -Filter '*.go' -File)
    $taskExpected = @(Get-ChildItem -LiteralPath (Join-Path $adminRoot 'internal/db/generated') -Filter '*.go' -File)
    if ($taskActual.Count -ne $taskExpected.Count) { throw 'Admin sqlc generated inventory drifted.' }
    foreach ($item in $taskActual) { Assert-GeneratedFile $item.FullName (Join-Path $adminRoot 'internal/db/generated' $item.Name) }
    $taskGenerator = Join-Path $adminRoot $(if ($IsWindows) { 'web/node_modules/.bin/openapi-typescript.cmd' } else { 'web/node_modules/.bin/openapi-typescript' })
    if (-not (Test-Path -LiteralPath $taskGenerator -PathType Leaf)) { throw 'Admin dependencies missing; run npm ci in hub/admin/web.' }
    $taskTs = Join-Path $taskTemp 'openapi.d.ts'
    & $taskGenerator $taskSource -o $taskTs
    if ($LASTEXITCODE -ne 0) { throw 'Admin OpenAPI TypeScript generation failed.' }
    Assert-GeneratedFile $taskTs (Join-Path $adminRoot 'web/src/lib/openapi.d.ts')
    Write-Output 'Admin Go/TypeScript OpenAPI and sqlc projections are deterministic and current.'
} finally {
    if (Test-Path -LiteralPath $taskTemp) {
        $taskResolved = (Resolve-Path -LiteralPath $taskTemp).Path
        if (-not [string]::Equals($taskResolved, [IO.Path]::GetFullPath($taskTemp), [StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($taskResolved) -notmatch '^zhvpn-admin-contract-[a-f0-9]{32}$') { throw 'Temporary cleanup target is outside the owned contract fixture.' }
        Remove-Item -LiteralPath $taskResolved -Recurse -Force
    }
}
