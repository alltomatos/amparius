# Amparius - homologacao de readiness completa com dependencias DESCARTAVEIS.
# Sobe PostgreSQL, Redis, RabbitMQ e um object storage S3-compatible (adobe/s3mock,
# SIMULADOR de S3: nao e MinIO/R2 reais) em portas proprias e roda
# scripts/smoke-readiness.ps1 nos dois modos: sem dependencias (503 esperado) e
# com todas (200 esperado). Nunca toca nos containers/portas do docker-compose.
#
# Uso: .\scripts\homolog-readiness.ps1
$ErrorActionPreference = "Continue"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$names = @("amparius-homolog-rd-pg", "amparius-homolog-rd-redis", "amparius-homolog-rd-mq", "amparius-homolog-rd-s3")
$pgPort = 55434; $redisPort = 56380; $mqPort = 55673; $s3Port = 59090

foreach ($n in $names) { if (docker ps -aq -f "name=^${n}$") { docker rm -f $n | Out-Null } }
$code = 1
try {
  docker run -d --name $names[0] -e POSTGRES_USER=amparius -e POSTGRES_PASSWORD=amparius -e POSTGRES_DB=amparius -p "${pgPort}:5432" postgres:16-alpine | Out-Null
  docker run -d --name $names[1] -p "${redisPort}:6379" redis:7-alpine | Out-Null
  docker run -d --name $names[2] -e RABBITMQ_DEFAULT_USER=amparius -e RABBITMQ_DEFAULT_PASS=amparius -p "${mqPort}:5672" rabbitmq:3.13-management-alpine | Out-Null
  docker run -d --name $names[3] -e COM_ADOBE_TESTING_S3MOCK_STORE_INITIAL_BUCKETS=amparius -p "${s3Port}:9090" adobe/s3mock | Out-Null
  for ($i = 0; $i -lt 40; $i++) {
    $null = docker exec $names[0] pg_isready -U amparius -d amparius
    $pgOk = $LASTEXITCODE
    # Nao usar `docker exec rabbitmq-diagnostics` na subida: o probe derruba o container.
    $mqOk = 1
    if ((docker logs $names[2] 2>&1 | Out-String) -match "Server startup complete") { $mqOk = 0 }
    $s3 = 0
    try { $s3 = (Invoke-WebRequest -UseBasicParsing -Method Head -Uri "http://127.0.0.1:${s3Port}/amparius").StatusCode } catch { $s3 = 0 }
    if ($pgOk -eq 0 -and $mqOk -eq 0 -and $s3 -eq 200) { break }
    Start-Sleep -Seconds 3
  }
  $dsn = "postgres://amparius:amparius@127.0.0.1:${pgPort}/amparius?sslmode=disable"

  Write-Host "== Modo 1: dependencias ausentes (Redis/RabbitMQ em portas fechadas) -> readyz 503"
  & .\scripts\smoke-readiness.ps1 -PostgresDsn $dsn
  if ($LASTEXITCODE -ne 0) { throw "smoke sem dependencias falhou" }

  Write-Host "== Modo 2: todas as dependencias de pe -> readyz 200"
  & .\scripts\smoke-readiness.ps1 -ExpectReady -PostgresDsn $dsn `
    -RedisAddress "127.0.0.1:${redisPort}" -RabbitMQUrl "amqp://amparius:amparius@127.0.0.1:${mqPort}/" `
    -ObjectStorageEndpoint "http://127.0.0.1:${s3Port}" -ObjectStorageBucket "amparius" `
    -ObjectStorageAccessKey "homolog" -ObjectStorageSecretKey "homolog-secret"
  if ($LASTEXITCODE -ne 0) { throw "smoke com todas as dependencias falhou" }
  $code = 0
}
catch { Write-Host "HOMOLOG READINESS FAIL: $_"; foreach ($n in $names) { Write-Host "-- $n"; docker ps -a --filter "name=^${n}$" --format "{{.Status}}"; docker logs --tail 5 $n 2>&1 | Out-Host } }
finally { foreach ($n in $names) { if (docker ps -aq -f "name=^${n}$") { docker rm -f $n | Out-Null } } }
if ($code -eq 0) { Write-Host "HOMOLOG READINESS PASS" }
exit $code
