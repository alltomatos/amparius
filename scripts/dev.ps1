# Amparius — helpers do ambiente local (Fase 0)
# Uso:
#   .\scripts\dev.ps1 up      -> docker compose up -d
#   .\scripts\dev.ps1 down    -> docker compose down
#   .\scripts\dev.ps1 logs    -> docker compose logs -f
#   .\scripts\dev.ps1 ps      -> docker compose ps
#   .\scripts\dev.ps1 reset   -> docker compose down -v (apaga volumes)

param(
  [Parameter(Position = 0)]
  [ValidateSet("up", "down", "logs", "ps", "reset")]
  [string]$Command = "up"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

switch ($Command) {
  "up"    { docker compose up -d }
  "down"  { docker compose down }
  "logs"  { docker compose logs -f }
  "ps"    { docker compose ps }
  "reset" { docker compose down -v }
}
