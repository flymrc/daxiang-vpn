# Fail closed on scanner/network/parse errors. Findings are evidence, not an
# assertion of exploitability. JSON govulncheck exit=0 alone is never a pass.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$EvidenceDirectory)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'npm-audit-policy.ps1')
$taskEvidence = [IO.Path]::GetFullPath($EvidenceDirectory)
if (Test-Path -LiteralPath $taskEvidence) { throw 'Use a fresh security evidence directory; old scanner reports must not be reused.' }
New-Item -ItemType Directory -Force -Path $taskEvidence | Out-Null
Push-Location $repoRoot
try {
    $previous = @{}
    foreach ($key in @('GOBIN','GOOS','GOARCH','CGO_ENABLED')) { $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
    try {
        $env:GOOS = (& go env GOHOSTOS).Trim(); $env:GOARCH = (& go env GOHOSTARCH).Trim(); $env:CGO_ENABLED = '0'
        $env:GOBIN = Join-Path $taskEvidence 'tools'
        New-Item -ItemType Directory -Path $env:GOBIN | Out-Null
        & go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
        if ($LASTEXITCODE -ne 0) { throw 'Pinned Go vulnerability scanner build failed.' }
        $scanner = Join-Path $env:GOBIN $(if ($IsWindows) { 'govulncheck.exe' } else { 'govulncheck' })
        $targets = @(@{os='windows';arch='amd64'},@{os='windows';arch='arm64'},@{os='linux';arch='amd64'},@{os='linux';arch='arm64'},@{os='darwin';arch='amd64'},@{os='darwin';arch='arm64'})
        foreach ($target in $targets) {
            $env:GOOS=$target.os; $env:GOARCH=$target.arch
            $name="govulncheck-$($target.os)-$($target.arch)"
            $raw=Join-Path $taskEvidence "$name.jsonl"
            & $scanner -tags with_gvisor -json ./... 1> $raw 2> (Join-Path $taskEvidence "$name.stderr.txt")
            if ($LASTEXITCODE -ne 0) { throw "Go vulnerability scanner failed for $name." }
            & python (Join-Path $PSScriptRoot 'security-report.py') $raw (Join-Path $taskEvidence "$name-summary.json") (Join-Path $PSScriptRoot 'security-exceptions.json')
            if ($LASTEXITCODE -ne 0) { throw "Go vulnerability gate failed for $name; original output retained." }
        }
    } finally {
        foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    }
    foreach ($frontend in @(@{path='clients/desktop-gui';report='npm-audit.json'},@{path='hub/admin/web';report='npm-audit-admin.json'})) {
        Invoke-ZhNpmAudit -ProjectDirectory (Join-Path $repoRoot $frontend.path) -ReportPath (Join-Path $taskEvidence $frontend.report)
    }
    Push-Location 'clients/desktop-gui'
    try {
        if (-not (Get-Command cargo-audit -ErrorAction SilentlyContinue)) { throw 'cargo-audit missing; install the reviewed locked scanner before release.' }
        & cargo audit --file src-tauri/Cargo.lock --json 1> (Join-Path $taskEvidence 'cargo-audit.json')
        if ($LASTEXITCODE -ne 0) { throw 'Rust dependency audit failed; raw report retained.' }
        $taskRustHost = ((& rustc -Vv) | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
        if (-not $taskRustHost) { throw 'Rust target identity unavailable.' }
        foreach ($target in @($taskRustHost,'x86_64-pc-windows-msvc','aarch64-pc-windows-msvc','x86_64-apple-darwin','aarch64-apple-darwin') | Select-Object -Unique) {
            $tree = Join-Path $taskEvidence "cargo-target-tree-$target.txt"
            & cargo tree --locked --manifest-path src-tauri/Cargo.toml --target $target --prefix none --no-dedupe 1> $tree
            if ($LASTEXITCODE -ne 0) { throw "Rust target dependency tree unavailable: $target." }
            & python (Join-Path $PSScriptRoot 'rust-security-report.py') (Join-Path $taskEvidence 'cargo-audit.json') (Join-Path $PSScriptRoot 'security-exceptions.json') $tree
            if ($LASTEXITCODE -ne 0) { throw "Rust warning review gate failed for $target." }
        }
    } finally { Pop-Location }
    Write-Host 'Security gates passed for this platform and database snapshot; signing/runtime acceptance are separate.'
} finally { Pop-Location }
