# Amparius — ambiente E2E local reproduzível (issue #58 / E2E-01A).
# Sobe Postgres/Redis/RabbitMQ, cria o papel de aplicação (credencial de DEV
# local; nunca segredo de produção — AGENTS.md #6), aplica as migrations da
# célula e do control plane do zero e carrega fixtures sintéticas de
# identidade/empresa/catálogo, sem dado pessoal real (AGENTS.md #5, LGPD).
#
# Uso:
#   .\scripts\bootstrap-e2e.ps1          -> sobe o que faltar e (re)aplica migrations + fixtures
#   .\scripts\bootstrap-e2e.ps1 -Reset   -> docker compose down -v antes (ambiente 100% limpo)

param(
  [switch]$Reset
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$PostgresUser = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { "amparius" }
$PostgresPassword = if ($env:POSTGRES_PASSWORD) { $env:POSTGRES_PASSWORD } else { "amparius" }
$PostgresDb = if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { "amparius" }
$PostgresPort = if ($env:POSTGRES_PORT) { $env:POSTGRES_PORT } else { "5432" }

$AppUser = if ($env:POSTGRES_APP_USER) { $env:POSTGRES_APP_USER } else { "amparius_app" }
$AppPassword = if ($env:POSTGRES_APP_PASSWORD) { $env:POSTGRES_APP_PASSWORD } else { "amparius_app" }
$ControlAppUser = if ($env:POSTGRES_CONTROL_APP_USER) { $env:POSTGRES_CONTROL_APP_USER } else { "amparius_control_app" }
$ControlAppPassword = if ($env:POSTGRES_CONTROL_APP_PASSWORD) { $env:POSTGRES_CONTROL_APP_PASSWORD } else { "amparius_control_app" }
$ControlDb = "amparius_control"
$GooseVersion = "v3.24.3"

function Invoke-Psql {
  param([string]$Database, [string]$Sql)
  $Sql | docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U $PostgresUser -d $Database
  if ($LASTEXITCODE -ne 0) { throw "psql falhou (banco $Database)" }
}

function New-AppRole {
  param([string]$Database, [string]$Role, [string]$Password)
  $sql = @"
DO `$do`$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$Role') THEN
    CREATE ROLE $Role LOGIN PASSWORD '$Password' NOSUPERUSER NOBYPASSRLS;
  END IF;
END
`$do`$;
"@
  Invoke-Psql -Database $Database -Sql $sql
}

if ($Reset) {
  Write-Host "==> docker compose down -v (ambiente 100% limpo)"
  docker compose down -v
  if ($LASTEXITCODE -ne 0) { throw "docker compose down -v falhou" }
}

Write-Host "==> docker compose up -d"
docker compose up -d
if ($LASTEXITCODE -ne 0) { throw "docker compose up falhou" }

Write-Host "==> aguardando Postgres ficar pronto"
$ready = $false
for ($i = 0; $i -lt 30; $i++) {
  docker compose exec -T postgres pg_isready -U $PostgresUser -d $PostgresDb *> $null
  if ($LASTEXITCODE -eq 0) { $ready = $true; break }
  Start-Sleep -Seconds 2
}
if (-not $ready) { throw "Postgres nao ficou pronto a tempo" }

Write-Host "==> papel de aplicacao da celula ($AppUser)"
New-AppRole -Database $PostgresDb -Role $AppUser -Password $AppPassword

Write-Host "==> migrations da celula (migrations/)"
go run "github.com/pressly/goose/v3/cmd/goose@$GooseVersion" -dir migrations postgres "postgres://${PostgresUser}:${PostgresPassword}@127.0.0.1:${PostgresPort}/${PostgresDb}?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose (celula) falhou" }

Write-Host "==> papel de aplicacao do control plane ($ControlAppUser)"
New-AppRole -Database $ControlDb -Role $ControlAppUser -Password $ControlAppPassword

Write-Host "==> migrations do control plane (migrations/control/)"
go run "github.com/pressly/goose/v3/cmd/goose@$GooseVersion" -dir migrations/control postgres "postgres://${PostgresUser}:${PostgresPassword}@127.0.0.1:${PostgresPort}/${ControlDb}?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose (control plane) falhou" }

Write-Host "==> fixtures sinteticas (identidade, empresa, catalogo; sem PII real)"
$fixtureSql = Get-Content (Join-Path $root "scripts\fixtures\e2e-seed.sql") -Raw
Invoke-Psql -Database $PostgresDb -Sql $fixtureSql

Write-Host "==> ambiente E2E pronto: Postgres/Redis/RabbitMQ no ar, migrations aplicadas (celula + control plane), fixtures carregadas sem PII real."
