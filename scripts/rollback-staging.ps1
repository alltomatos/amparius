# Rollback seguro do binário da API em staging.
# Banco não sofre "down" de migration: o rollback de schema é forward-only e
# exige um plano/migration compatível, ou restore explícito a partir de backup.

param(
  [Parameter(Position = 0)]
  [ValidateSet("plan", "apply")]
  [string]$Action = "plan",
  [Parameter(Mandatory = $false)]
  [string]$PreviousImage = $env:AMPARIUS_PREVIOUS_API_IMAGE,
  [Parameter(Mandatory = $false)]
  [string]$BackupFile = $env:AMPA_BACKUP_FILE,
  [switch]$AllowRollback
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$staging = Join-Path $root "deploy/staging"
$composeFile = Join-Path $staging "docker-compose.yml"
$envFile = Join-Path $staging ".env"

if ([string]::IsNullOrWhiteSpace($PreviousImage)) {
  throw "PreviousImage é obrigatório (ou AMPARIUS_PREVIOUS_API_IMAGE)"
}
if (-not (Test-Path -LiteralPath $composeFile)) {
  throw "compose de staging não encontrado"
}
$composeArgs = @("compose", "--env-file", $envFile, "-f", $composeFile)

if ($Action -eq "plan") {
  Write-Host "ROLLBACK PLAN"
  Write-Host "- preservar o backup indicado antes da troca: $BackupFile"
  Write-Host "- parar somente api"
  Write-Host "- iniciar a imagem anterior: $PreviousImage"
  Write-Host "- verificar /healthz e /readyz uma vez"
  Write-Host "- não executar goose down nem apagar volumes"
  exit 0
}

if (-not $AllowRollback) {
  throw "rollback altera o serviço; repita com -AllowRollback"
}
if (-not (Test-Path -LiteralPath $envFile)) {
  throw "deploy/staging/.env não encontrado; não crie credenciais neste script"
}
if ([string]::IsNullOrWhiteSpace($BackupFile) -or -not (Test-Path -LiteralPath $BackupFile)) {
  throw "backup existente é obrigatório antes do rollback"
}

& pg_restore --list $BackupFile | Out-Null
if ($LASTEXITCODE -ne 0) { throw "arquivo de backup inválido" }

$oldImage = $env:API_IMAGE
try {
  $env:API_IMAGE = $PreviousImage
  Write-Host "==> parando apenas api"
  & docker @composeArgs stop api
  if ($LASTEXITCODE -ne 0) { throw "falha ao parar api" }

  Write-Host "==> iniciando imagem anterior"
  & docker @composeArgs up -d --no-build api
  if ($LASTEXITCODE -ne 0) { throw "falha ao iniciar imagem anterior" }

  Write-Host "ROLLBACK APPLIED: imagem anterior iniciada; probes devem ser verificados no endpoint configurado"
}
finally {
  if ($null -eq $oldImage) { Remove-Item Env:API_IMAGE -ErrorAction SilentlyContinue }
  else { $env:API_IMAGE = $oldImage }
}
