param([switch]$Frontend,[switch]$Race)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $root
$env:GOCACHE = Join-Path $root '.cache/go-build'
$env:GOMODCACHE = Join-Path $root '.cache/go-mod'
$env:GOTOOLCHAIN = 'local'
if ($Frontend) {
 Push-Location (Join-Path $root 'web')
 try {
  & npm.cmd run typecheck
  if ($LASTEXITCODE -ne 0) { throw 'Typecheck failed' }
  & npm.cmd test -- --run
  if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed' }
  & npm.cmd run build
  if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
 } finally { Pop-Location }
 & node web/scripts/check-report-runtime.mjs
 if ($LASTEXITCODE -ne 0) { throw 'Offline report check failed' }
}
& go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
& go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
if ($Race) {
 & go test -race ./...
 if ($LASTEXITCODE -ne 0) { throw 'Race tests failed' }
}
