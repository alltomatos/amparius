# Smoke local da separação liveness/readiness.
# PostgreSQL deve estar disponível; as outras portas são intencionalmente
# fechadas para provar o 503 sem depender de todos os serviços.

param(
  [switch]$ExpectReady,
  [string]$RedisAddress = "127.0.0.1:6399",
  [string]$RabbitMQUrl = "amqp://127.0.0.1:5673/",
  [string]$ObjectStorageEndpoint = "",
  [string]$ObjectStorageRegion = "auto",
  [string]$ObjectStorageBucket = "amparius",
  [string]$ObjectStorageAccessKey = "",
  [string]$ObjectStorageSecretKey = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$port = 18081
$exe = Join-Path ([System.IO.Path]::GetTempPath()) "amparius-api-readiness-smoke.exe"
$stdout = Join-Path ([System.IO.Path]::GetTempPath()) "amparius-api-readiness-smoke.out"
$stderr = Join-Path ([System.IO.Path]::GetTempPath()) "amparius-api-readiness-smoke.err"
$process = $null
$names = @("APP_ENV", "HTTP_PORT", "POSTGRES_DSN", "REDIS_ADDR", "RABBITMQ_URL", "OBJECT_STORAGE_ENDPOINT", "OBJECT_STORAGE_REGION", "OBJECT_STORAGE_BUCKET", "OBJECT_STORAGE_ACCESS_KEY", "OBJECT_STORAGE_SECRET_KEY", "OBJECT_STORAGE_USE_PATH_STYLE", "ENABLE_DEV_ONBOARDING")
$old = @{}
foreach ($name in $names) { $old[$name] = [Environment]::GetEnvironmentVariable($name) }

try {
  go build -o $exe .\cmd\api
  $env:APP_ENV = "test"
  $env:HTTP_PORT = "$port"
  $env:POSTGRES_DSN = "postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable"
  $env:REDIS_ADDR = $RedisAddress
  $env:RABBITMQ_URL = $RabbitMQUrl
  $env:OBJECT_STORAGE_ENDPOINT = $ObjectStorageEndpoint
  $env:OBJECT_STORAGE_REGION = $ObjectStorageRegion
  $env:OBJECT_STORAGE_BUCKET = $ObjectStorageBucket
  $env:OBJECT_STORAGE_ACCESS_KEY = $ObjectStorageAccessKey
  $env:OBJECT_STORAGE_SECRET_KEY = $ObjectStorageSecretKey
  $env:OBJECT_STORAGE_USE_PATH_STYLE = "true"
  $env:ENABLE_DEV_ONBOARDING = "false"

  $process = Start-Process -FilePath $exe -WorkingDirectory $root -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
  Start-Sleep -Milliseconds 700

  $health = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/healthz"
  if ($health.StatusCode -ne 200) { throw "healthz retornou $($health.StatusCode)" }

  $expectedStatus = if ($ExpectReady) { 200 } else { 503 }
  try {
    $ready = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/readyz"
    if ($ready.StatusCode -ne $expectedStatus) { throw "readyz respondeu $($ready.StatusCode), esperado $expectedStatus" }
  }
  catch {
    if ($ExpectReady -or $_.Exception.Response.StatusCode.value__ -ne $expectedStatus) { throw }
  }
  Write-Host "READINESS SMOKE PASS: healthz=200 readyz=$expectedStatus"
}
finally {
  if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force }
  foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $old[$name]) }
  Remove-Item -LiteralPath $exe, $stdout, $stderr -Force -ErrorAction SilentlyContinue
}
