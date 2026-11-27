# Amparius - homologacao do control plane (API + UI) com identidade de TESTE em
# PostgreSQL DESCARTAVEL (porta 55435, sem volume). Nunca toca no PostgreSQL
# compartilhado (5432). OIDC/MFA reais continuam adiados: a identidade de teste
# existe so em cmd/api/homolog_control_plane_ui_test.go e nao entra no binario.
#
# Uso:
#   .\scripts\homolog-control-plane-ui.ps1               # roda os testes de API e remove o container
#   .\scripts\homolog-control-plane-ui.ps1 -Serve        # tambem serve a UI em http://127.0.0.1:18095
#                                                        # ate existir o arquivo de parada (ver saida)
param([switch]$Serve, [string]$Addr = "127.0.0.1:18095")

$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$name = "amparius-homolog-cp-ui"
$port = 55435
$goose = "github.com/pressly/goose/v3/cmd/goose@v3.24.3"

function Invoke-Psql([string]$Database, [string]$Sql) {
  $Sql | docker exec -i $name psql -v ON_ERROR_STOP=1 -U amparius -d $Database
  if ($LASTEXITCODE -ne 0) { throw "psql falhou ($Database)" }
}

if (docker ps -aq -f "name=^${name}$") { docker rm -f $name | Out-Null }
$code = 1
try {
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
  # Grants de FIXTURE (somente neste container descartavel), como em homolog-integration.ps1.
  Invoke-Psql "amparius" "GRANT INSERT, DELETE ON identity.tenant, identity.company, identity.establishment TO amparius_app"
  Invoke-Psql "amparius_control" "GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA entitlement TO amparius_control_app"

  $env:AMPA_TEST_POSTGRES_DSN = "postgres://amparius_app:amparius_app@127.0.0.1:${port}/amparius?sslmode=disable"
  $env:AMPA_TEST_POSTGRES_ADMIN_DSN = "postgres://amparius:amparius@127.0.0.1:${port}/amparius?sslmode=disable"
  $env:AMPA_CONTROL_POSTGRES_DSN = "postgres://amparius_control_app:amparius_control_app@127.0.0.1:${port}/amparius_control?sslmode=disable"

  Write-Host "== API do control plane com identidade de teste + matriz 404/503"
  go test ./cmd/api -count=1 -v -run 'TestControlPlane'
  if ($LASTEXITCODE -ne 0) { throw "testes de API do control plane falharam" }

  if ($Serve) {
    $stop = Join-Path ([System.IO.Path]::GetTempPath()) "amparius-homolog-ui.stop"
    Remove-Item -LiteralPath $stop -Force -ErrorAction SilentlyContinue
    $env:AMPA_HOMOLOG_UI_ADDR = $Addr
    $env:AMPA_HOMOLOG_UI_STOP = $stop
    Write-Host "== UI em http://$Addr/web/control-plane/index.html (crie $stop para encerrar)"
    go test ./cmd/api -count=1 -v -timeout 40m -run 'TestHomologControlPlaneUIServe'
    if ($LASTEXITCODE -ne 0) { throw "servidor da UI falhou" }
  }
  $code = 0
}
catch { Write-Host "HOMOLOG CONTROL PLANE FAIL: $_" }
finally { if (docker ps -aq -f "name=^${name}$") { docker rm -f $name | Out-Null } }
if ($code -eq 0) { Write-Host "HOMOLOG CONTROL PLANE PASS" }
exit $code
