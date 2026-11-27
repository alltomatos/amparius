# Amparius - homologacao de integracao em PostgreSQL DESCARTAVEL.
# Sobe um container proprio (porta 55432, sem volume), migra celula e control
# plane do zero e roda `go test ./...` com os DSNs de integracao. Nunca toca no
# PostgreSQL compartilhado do docker-compose (porta 5432).
#
# Grants de FIXTURE (somente neste container descartavel): os testes criam e
# removem tenants/empresas/licencas com o papel de aplicacao, entao ele recebe
# DML em identity.tenant/company e no schema entitlement. Em producao esses
# grants NAO existem; as migrations concedem so o minimo (ver AGENTS.md).
#
# Uso: .\scripts\homolog-integration.ps1 [-Keep]   (-Keep mantem o container)
param([switch]$Keep)

# Continue: goose/go/psql escrevem progresso em stderr; o controle e por $LASTEXITCODE.
$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$name = "amparius-homolog-pg"
$port = 55432
$goose = "github.com/pressly/goose/v3/cmd/goose@v3.24.3"

function Invoke-Psql([string]$Database, [string]$Sql) {
  $Sql | docker exec -i $name psql -v ON_ERROR_STOP=1 -U amparius -d $Database
  if ($LASTEXITCODE -ne 0) { throw "psql falhou ($Database)" }
}

if (docker ps -aq -f "name=^${name}$") { docker rm -f $name | Out-Null }
docker run -d --name $name -e POSTGRES_USER=amparius -e POSTGRES_PASSWORD=amparius -e POSTGRES_DB=amparius -p "${port}:5432" postgres:16-alpine | Out-Null
for ($i = 0; $i -lt 30; $i++) {
  $null = docker exec $name pg_isready -U amparius -d amparius
  if ($LASTEXITCODE -eq 0) { break }
  Start-Sleep -Seconds 2
}
Invoke-Psql "amparius" "CREATE DATABASE amparius_control OWNER amparius"
foreach ($r in @(@("amparius", "amparius_app"), @("amparius_control", "amparius_control_app"))) {
  Invoke-Psql $r[0] "CREATE ROLE $($r[1]) LOGIN PASSWORD '$($r[1])' NOSUPERUSER NOBYPASSRLS"
}
go run $goose -dir migrations postgres "postgres://amparius:amparius@127.0.0.1:${port}/amparius?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose celula falhou" }
go run $goose -dir migrations/control postgres "postgres://amparius:amparius@127.0.0.1:${port}/amparius_control?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose control falhou" }
Invoke-Psql "amparius" "GRANT INSERT, DELETE ON identity.tenant, identity.company, identity.establishment TO amparius_app"
Invoke-Psql "amparius_control" "GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA entitlement TO amparius_control_app"

# Bancos da #79 (cmd/api/store_dashboard_integration_test.go): o teste conecta
# como papel de aplicacao SEM senha, entao este container descartavel usa
# `trust` no pg_hba (so existe aqui, publicado em loopback no Docker).
docker exec $name sh -c "sed -i '1i host all all all trust' /var/lib/postgresql/data/pg_hba.conf"
Invoke-Psql "postgres" "SELECT pg_reload_conf()"
Invoke-Psql "postgres" "CREATE DATABASE issue79_cell OWNER amparius"
Invoke-Psql "postgres" "CREATE DATABASE issue79_control OWNER amparius"
go run $goose -dir migrations postgres "postgres://amparius:amparius@127.0.0.1:${port}/issue79_cell?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose issue79_cell falhou" }
go run $goose -dir migrations/control postgres "postgres://amparius:amparius@127.0.0.1:${port}/issue79_control?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose issue79_control falhou" }
$env:AMPA_ISSUE79_CELL_ADMIN_DSN = "postgres://amparius:amparius@127.0.0.1:${port}/issue79_cell?sslmode=disable"
$env:AMPA_ISSUE79_CONTROL_ADMIN_DSN = "postgres://amparius:amparius@127.0.0.1:${port}/issue79_control?sslmode=disable"
$env:READINESS_P95_ITERATIONS = "200"

$env:AMPA_TEST_POSTGRES_DSN ="postgres://amparius_app:amparius_app@127.0.0.1:${port}/amparius?sslmode=disable"
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = "postgres://amparius:amparius@127.0.0.1:${port}/amparius?sslmode=disable"
$env:AMPA_CONTROL_POSTGRES_DSN = "postgres://amparius_control_app:amparius_control_app@127.0.0.1:${port}/amparius_control?sslmode=disable"
go test ./... -count=1 -p 1
$code = $LASTEXITCODE
if (-not $Keep) { docker rm -f $name | Out-Null }
exit $code
