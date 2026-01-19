Param(
  [string]$Root = (Resolve-Path (Join-Path $PSScriptRoot ".."))
)

$ErrorActionPreference = "Stop"

Set-Location $Root

if (-not (Get-Command buf -ErrorAction SilentlyContinue)) {
  Write-Error "buf is not installed or not on PATH. Install with: go install github.com/bufbuild/buf/cmd/buf@latest"
}

buf --version
buf generate

