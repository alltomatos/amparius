# Amparius - verificação e build do SaaS Go.
# Uso:
#   .\scripts\build.ps1 check   -> go vet + go test + go build (padrão)
#   .\scripts\build.ps1 build   -> go build ./...
#   .\scripts\build.ps1 vet     -> go vet ./...
#   .\scripts\build.ps1 test    -> go test ./...
#   .\scripts\build.ps1 docker  -> docker build das imagens api e worker
#   .\scripts\build.ps1 all     -> check + docker

param(
  [Parameter(Position = 0)]
  [ValidateSet("check", "build", "vet", "test", "docker", "all")]
  [string]$Command = "check"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Invoke-GoBuild {
  Write-Host "==> go build ./..."
  go build ./...
}

function Invoke-GoVet {
  Write-Host "==> go vet ./..."
  go vet ./...
}

function Invoke-GoTest {
  Write-Host "==> go test ./..."
  go test ./...
}

function Invoke-Check {
  Invoke-GoVet
  Invoke-GoTest
  Invoke-GoBuild
}

function Invoke-Docker {
  Write-Host "==> docker build amparius-api:dev"
  docker build --build-arg SERVICE=api -t amparius-api:dev .
  if ($LASTEXITCODE -ne 0) { throw "falha ao construir a imagem api" }

  Write-Host "==> docker build amparius-worker:dev"
  docker build --build-arg SERVICE=worker -t amparius-worker:dev .
  if ($LASTEXITCODE -ne 0) { throw "falha ao construir a imagem worker" }
}

switch ($Command) {
  "check"  { Invoke-Check }
  "build"  { Invoke-GoBuild }
  "vet"    { Invoke-GoVet }
  "test"   { Invoke-GoTest }
  "docker" { Invoke-Docker }
  "all"    { Invoke-Check; Invoke-Docker }
}
