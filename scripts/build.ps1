param([string]$Version = '0.1.0')
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $root
$env:GOCACHE = Join-Path $root '.cache/go-build'
$env:GOMODCACHE = Join-Path $root '.cache/go-mod'
$env:GOTOOLCHAIN = 'local'
$env:npm_config_cache = Join-Path $root '.cache/npm'
if (!(Test-Path -LiteralPath (Join-Path $root 'web/package-lock.json'))) { throw 'Build incomplete: a real resolved web/package-lock.json is required. No placeholder frontend can be packaged.' }
Push-Location (Join-Path $root 'web')
try {
    & npm.cmd ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    & npm.cmd run typecheck
    if ($LASTEXITCODE -ne 0) { throw 'TypeScript validation failed' }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    if (!(Test-Path -LiteralPath 'dist/index.html')) { throw 'Real frontend dist/index.html missing' }
    foreach ($asset in @('report-dist/report.js','report-dist/report.css')) {
        if (!(Test-Path -LiteralPath $asset)) { throw "Real offline report asset missing: $asset" }
    }
} finally { Pop-Location }
function Get-CompilerInputFiles {
    param([string]$ProjectRoot)
    $pending = New-Object 'System.Collections.Generic.Stack[string]'
    foreach ($directory in @('cmd','internal','web')) { $pending.Push((Join-Path $ProjectRoot $directory)) }
    while ($pending.Count -gt 0) {
        foreach ($entry in Get-ChildItem -LiteralPath $pending.Pop() -Force -ErrorAction Stop) {
            if (($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { continue }
            if ($entry.PSIsContainer) {
                if ($entry.Name -cne 'node_modules') { $pending.Push($entry.FullName) }
            } elseif ($entry.Name -cmatch '\.(go|ts|tsx|css|html|js|mjs|json|svg|png|ico|woff.*)$') {
                $entry.FullName.Substring($ProjectRoot.TrimEnd('\','/').Length + 1).Replace('\','/')
            }
        }
    }
}
$sourceFiles = @(Get-CompilerInputFiles -ProjectRoot $root) + @('go.mod','go.sum','scripts/build.ps1')
$manifest = @($sourceFiles | Sort-Object -Unique | ForEach-Object { [ordered]@{ path = $_.Replace('\','/'); sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $_).Hash.ToLowerInvariant() } })
$digestInput = "version=$Version`n" + (($manifest | ForEach-Object { $_.path + ':' + $_.sha256 }) -join "`n")
$sha = [System.Security.Cryptography.SHA256]::Create()
try { $manifestHash = ([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($digestInput)))).Replace('-','').ToLowerInvariant() } finally { $sha.Dispose() }
New-Item -ItemType Directory -Force -Path (Join-Path $root 'logs') | Out-Null
@{ version = $Version; sourceDigest = $manifestHash; inputs = $manifest } | ConvertTo-Json -Depth 5 | Set-Content -Encoding UTF8 -LiteralPath (Join-Path $root 'logs/build-input-manifest.json')
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
New-Item -ItemType Directory -Force -Path (Join-Path $root 'dist') | Out-Null
& go build -trimpath -ldflags "-H=windowsgui -X highloader/internal/bootstrap.Version=$Version -X highloader/internal/bootstrap.SourceDigest=$manifestHash" -o dist/highloader.exe ./cmd/highloader
if ($LASTEXITCODE -ne 0) { throw 'Go release build failed' }
Get-FileHash -Algorithm SHA256 -LiteralPath dist/highloader.exe
