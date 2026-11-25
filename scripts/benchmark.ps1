# Mede o p95 do endpoint /readyz em servidor HTTP local e sem dependências reais.
# A medição é do caminho HTTP + orquestração do probe; não substitui o smoke
# com docker compose que valida as dependências reais.

param(
  [int]$Iterations = 1000
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

if ($Iterations -lt 100) {
  throw "Iterations deve ser >= 100"
}

$oldIterations = $env:READINESS_P95_ITERATIONS
try {
  $env:READINESS_P95_ITERATIONS = "$Iterations"
  Write-Host "==> readiness p95 ($Iterations amostras; dependência sintética)"
  go test ./internal/platform/http -run '^TestReadyzP95$' -count=1 -v
  if ($LASTEXITCODE -ne 0) {
    throw "falha na medição de readiness p95"
  }
}
finally {
  if ($null -eq $oldIterations) {
    Remove-Item Env:READINESS_P95_ITERATIONS -ErrorAction SilentlyContinue
  }
  else {
    $env:READINESS_P95_ITERATIONS = $oldIterations
  }
}
