param()
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$source = Join-Path $repoRoot 'hub/internal/deviceapi/spec/openapi.yaml'
$expected = Join-Path $repoRoot 'shared/devicecontract/types.gen.go'
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('zhvpn-device-contract-' + [guid]::NewGuid().ToString('N') + '.go')
try {
    & go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 --generate types --package devicecontract -o $temporary $source
    if ($LASTEXITCODE -ne 0) { throw 'device authority contract generation failed' }
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $temporary).Hash
    $expectedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $expected).Hash
    if ($actualHash -ne $expectedHash) { throw 'generated v2 device types drifted from canonical OpenAPI; run go generate ./hub/internal/deviceapi' }
    Write-Output 'Device v2 canonical OpenAPI generation is deterministic and current.'
} finally {
    if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary }
}
