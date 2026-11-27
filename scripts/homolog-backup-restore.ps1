# Amparius - homologacao de backup/restore em PostgreSQL DESCARTAVEL.
# Sobe um container proprio (porta 55433, sem volume), migra a celula e o control
# plane do zero, insere dados SINTETICOS, faz pg_dump -Fc e pg_restore em outro
# banco e compara a estrutura (tabelas, RLS, policies, grants, versao goose) e
# os dados. Nunca toca no PostgreSQL compartilhado (porta 5432).
#
# As ferramentas do cliente PostgreSQL rodam DENTRO do container (o host de
# desenvolvimento nao precisa de psql/pg_dump). Complementa
# scripts/backup-restore.ps1, que exige essas ferramentas no host.
#
# Uso: .\scripts\homolog-backup-restore.ps1 [-Keep]
param([switch]$Keep)

$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$name = "amparius-homolog-bk"
$port = 55433
$goose = "github.com/pressly/goose/v3/cmd/goose@v3.24.3"
$failures = New-Object System.Collections.Generic.List[string]

function Invoke-Psql([string]$Database, [string]$Sql) {
  $Sql | docker exec -i $name psql -v ON_ERROR_STOP=1 -U amparius -d $Database
  if ($LASTEXITCODE -ne 0) { throw "psql falhou ($Database)" }
}
function Get-Scalar([string]$Database, [string]$Sql) {
  $out = $Sql | docker exec -i $name psql -v ON_ERROR_STOP=1 -tA -U amparius -d $Database
  if ($LASTEXITCODE -ne 0) { throw "psql falhou ($Database)" }
  return (($out | Out-String).Trim())
}
function Assert-Equal([string]$Label, [string]$Source, [string]$Restored) {
  if ($Source -eq $Restored) { Write-Host ("PASS  {0}: {1}" -f $Label, $Source) }
  else { Write-Host ("FAIL  {0}: origem={1} restaurado={2}" -f $Label, $Source, $Restored); $failures.Add($Label) }
}

if (docker ps -aq -f "name=^${name}$") { docker rm -f $name | Out-Null }
docker run -d --name $name -e POSTGRES_USER=amparius -e POSTGRES_PASSWORD=amparius -e POSTGRES_DB=amparius -p "${port}:5432" postgres:16-alpine | Out-Null
for ($i = 0; $i -lt 30; $i++) {
  $null = docker exec $name pg_isready -U amparius -d amparius
  if ($LASTEXITCODE -eq 0) { break }
  Start-Sleep -Seconds 2
}
Invoke-Psql "amparius" "CREATE ROLE amparius_app LOGIN PASSWORD 'amparius_app' NOSUPERUSER NOBYPASSRLS"
go run $goose -dir migrations postgres "postgres://amparius:amparius@127.0.0.1:${port}/amparius?sslmode=disable" up
if ($LASTEXITCODE -ne 0) { throw "goose falhou" }

# Dados sinteticos (somente neste container).
$tenant = "018f0000-0000-7000-8000-000000000001"
Invoke-Psql "amparius" @"
INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ('$tenant', 'Tenant Sintetico', '00000000000001', 'simples', 'active');
INSERT INTO fiscal.tax_rule (id, tenant_id, tax_type, regime, scope_kind, ncm, class_trib, basis_kind, rate, rate_unit, valid_from, valid_to, source_kind, source_name, source_url, source_version, source_published_at, source_hash, import_batch)
VALUES (gen_random_uuid(), NULL, 'is', 'geral', 'nfe', '11111111', 'CLASSE-TESTE', 'isencao', NULL, NULL, '2026-01-01', '2027-01-01', 'rfb', 'Teste sintetico', 'https://invalid.test', 'teste', '2026-01-01', repeat('a',64), gen_random_uuid());
"@

# Backup (formato custom, COM ACLs: sem isso os GRANTs das migrations se perdem).
docker exec $name sh -c "pg_dump -U amparius -Fc --no-owner -d amparius -f /tmp/amparius.dump && pg_restore --list /tmp/amparius.dump >/dev/null"
if ($LASTEXITCODE -ne 0) { throw "pg_dump falhou" }
$sha = (docker exec $name sh -c "sha256sum /tmp/amparius.dump").Split(" ")[0]
Write-Host "BACKUP PASS: sha256=$sha"

# Restore em banco novo.
Invoke-Psql "postgres" "CREATE DATABASE amparius_restored OWNER amparius"
docker exec $name sh -c "pg_restore -U amparius --no-owner -d amparius_restored /tmp/amparius.dump"
if ($LASTEXITCODE -ne 0) { throw "pg_restore falhou" }
Write-Host "RESTORE PASS: dump restaurado em amparius_restored"

$checks = [ordered]@{
  "versao goose"            = "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied"
  "tabelas (nao-sistema)"   = "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')"
  "tabelas com RLS"         = "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND c.relrowsecurity AND n.nspname NOT IN ('pg_catalog','information_schema')"
  "tabelas com FORCE RLS"   = "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND c.relforcerowsecurity AND n.nspname NOT IN ('pg_catalog','information_schema')"
  "policies"                = "SELECT count(*) FROM pg_policies"
  "triggers de usuario"     = "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal"
  "indices"                 = "SELECT count(*) FROM pg_indexes WHERE schemaname NOT IN ('pg_catalog','information_schema')"
  "grants de tabela do app" = "SELECT count(*) FROM information_schema.role_table_grants WHERE grantee='amparius_app'"
  "hash dos grants do app"  = "SELECT md5(string_agg(table_schema||'.'||table_name||':'||privilege_type, ',' ORDER BY table_schema||'.'||table_name||':'||privilege_type)) FROM information_schema.role_table_grants WHERE grantee='amparius_app'"
  "linhas identity.tenant"  = "SELECT count(*) FROM identity.tenant"
  "linhas fiscal.tax_rule"  = "SELECT count(*) FROM fiscal.tax_rule"
}
foreach ($k in $checks.Keys) {
  Assert-Equal $k (Get-Scalar "amparius" $checks[$k]) (Get-Scalar "amparius_restored" $checks[$k])
}

# O banco restaurado tem de continuar isolando tenants para o papel da aplicacao
# (controle positivo: o dono do dado enxerga 1; tenant alheio enxerga 0).
function Get-AsApp([string]$TenantId) {
  $q = "BEGIN; SELECT set_config('app.tenant_id','$TenantId',true); SELECT 'n=' || count(*) FROM identity.tenant; ROLLBACK;"
  $out = docker exec $name psql -tA -U amparius_app -d amparius_restored -c $q 2>&1 | Out-String
  if ($out -match 'n=(\d+)') { return $Matches[1] }
  return "erro: $out"
}
Assert-Equal "RLS no restaurado (dono ve 1 tenant)" "1" (Get-AsApp $tenant)
Assert-Equal "RLS no restaurado (tenant alheio ve 0)" "0" (Get-AsApp "018f0000-0000-7000-8000-0000000000ff")
# Controle negativo: scripts/backup-restore.ps1 usava `--no-acl` (corrigido na
# homologacao de 2026-11-27). Mede o que isso faz com os GRANTs das migrations
# (informativo; nao reprova a rodada).
docker exec $name sh -c "pg_dump -U amparius -Fc --no-owner --no-acl -d amparius -f /tmp/noacl.dump"
Invoke-Psql "postgres" "CREATE DATABASE amparius_noacl OWNER amparius"
docker exec $name sh -c "pg_restore -U amparius --no-owner --no-acl -d amparius_noacl /tmp/noacl.dump"
$grantsSrc = Get-Scalar "amparius" $checks["grants de tabela do app"]
$grantsNoAcl = Get-Scalar "amparius_noacl" $checks["grants de tabela do app"]
Write-Host "INFO  dump/restore com --no-acl: grants do app origem=$grantsSrc restaurado=$grantsNoAcl (0 = o app perde acesso apos restore)"
if (-not $Keep) { docker rm -f $name | Out-Null }
if ($failures.Count -gt 0) { Write-Host ("HOMOLOG BACKUP/RESTORE FAIL: " + ($failures -join "; ")); exit 1 }
Write-Host "HOMOLOG BACKUP/RESTORE PASS: estrutura, RLS, grants e dados identicos apos restore"
exit 0
