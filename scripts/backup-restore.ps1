# Backup, restore e teste destrutivo controlado do PostgreSQL do Amparius.
#
# O modo test cria um banco temporário, insere somente dados sintéticos,
# executa pg_dump + pg_restore e remove o banco ao final. Não use o modo
# restore sem -AllowDestructiveRestore.

param(
  [Parameter(Position = 0)]
  [ValidateSet("backup", "restore", "test")]
  [string]$Action = "test",
  [string]$Dsn = $env:AMPA_BACKUP_DSN,
  [string]$AdminDsn = $env:AMPA_BACKUP_TEST_ADMIN_DSN,
  [string]$BackupFile = $env:AMPA_BACKUP_FILE,
  [switch]$AllowDestructiveRestore
)

$ErrorActionPreference = "Stop"

function Require-Command([string]$Name) {
  if ($null -eq (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "comando obrigatório não encontrado: $Name"
  }
}

function Require-Value([string]$Name, [string]$Value) {
  if ([string]::IsNullOrWhiteSpace($Value)) {
    throw "$Name é obrigatório e deve ser fornecido por variável de ambiente ou parâmetro"
  }
}

function Invoke-Native([string]$Command, [string[]]$Arguments) {
  & $Command @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "$Command falhou com código $LASTEXITCODE"
  }
}

function Database-Dsn([string]$BaseDsn, [string]$Database) {
  if ($BaseDsn -notmatch '^(?<prefix>.+/)[^/?]+(?<suffix>\?.*)?$') {
    throw "DSN deve usar URI PostgreSQL para trocar o banco com segurança"
  }
  return $Matches.prefix + $Database + $Matches.suffix
}

function New-Database([string]$BaseDsn, [string]$Database) {
  $sql = 'CREATE DATABASE "' + $Database + '"'
  Invoke-Native "psql" @("--dbname=$BaseDsn", "-v", "ON_ERROR_STOP=1", "-c", $sql)
}

function Remove-Database([string]$BaseDsn, [string]$Database) {
  $sql = 'DROP DATABASE IF EXISTS "' + $Database + '" WITH (FORCE)'
  Invoke-Native "psql" @("--dbname=$BaseDsn", "-v", "ON_ERROR_STOP=1", "-c", $sql)
}

function Dump-Database([string]$SourceDsn, [string]$TargetFile) {
  $parent = Split-Path -Parent $TargetFile
  if (-not [string]::IsNullOrWhiteSpace($parent)) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
  }
  Invoke-Native "pg_dump" @(
    "--format=custom",
    "--no-owner",
    "--no-acl",
    "--file=$TargetFile",
    "--dbname=$SourceDsn"
  )
  if (-not (Test-Path -LiteralPath $TargetFile)) {
    throw "pg_dump não produziu o arquivo de backup"
  }
  Invoke-Native "pg_restore" @("--list", $TargetFile)
}

Require-Command "psql"
Require-Command "pg_dump"
Require-Command "pg_restore"

switch ($Action) {
  "backup" {
    Require-Value "AMPA_BACKUP_DSN" $Dsn
    if ([string]::IsNullOrWhiteSpace($BackupFile)) {
      $BackupFile = Join-Path ([System.IO.Path]::GetTempPath()) "amparius-backup.dump"
    }
    Dump-Database $Dsn $BackupFile
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $BackupFile).Hash
    Write-Host "BACKUP PASS: arquivo criado; sha256=$hash"
  }
  "restore" {
    Require-Value "AMPA_BACKUP_DSN" $Dsn
    Require-Value "AMPA_BACKUP_FILE" $BackupFile
    if (-not $AllowDestructiveRestore) {
      throw "restore é destrutivo; repita com -AllowDestructiveRestore"
    }
    if (-not (Test-Path -LiteralPath $BackupFile)) {
      throw "arquivo de backup não encontrado"
    }
    Invoke-Native "pg_restore" @(
      "--clean",
      "--if-exists",
      "--no-owner",
      "--no-acl",
      "--dbname=$Dsn",
      $BackupFile
    )
    Write-Host "RESTORE PASS: objetos do dump restaurados no banco informado"
  }
  "test" {
    Require-Value "AMPA_BACKUP_TEST_ADMIN_DSN" $AdminDsn
    $probeDatabase = "amparius_backup_probe_" + ([guid]::NewGuid().ToString("N").Substring(0, 12))
    $probeDsn = Database-Dsn $AdminDsn $probeDatabase
    $probeFile = Join-Path ([System.IO.Path]::GetTempPath()) ($probeDatabase + ".dump")
    try {
      New-Database $AdminDsn $probeDatabase
      Invoke-Native "psql" @(
        "--dbname=$probeDsn", "-v", "ON_ERROR_STOP=1", "-c",
        "CREATE TABLE backup_probe (id integer PRIMARY KEY, marker text NOT NULL); INSERT INTO backup_probe VALUES (1, 'synthetic-only');"
      )
      Dump-Database $probeDsn $probeFile
      Remove-Database $AdminDsn $probeDatabase
      New-Database $AdminDsn $probeDatabase
      Invoke-Native "pg_restore" @("--no-owner", "--no-acl", "--dbname=$probeDsn", $probeFile)
      $marker = (& psql "--dbname=$probeDsn" "-v" "ON_ERROR_STOP=1" "-tA" "-c" "SELECT marker FROM backup_probe WHERE id = 1").Trim()
      if ($LASTEXITCODE -ne 0 -or $marker -ne "synthetic-only") {
        throw "verificação do restore não encontrou o marcador sintético"
      }
      Write-Host "BACKUP/RESTORE TEST PASS: banco temporário restaurado e verificado; nenhum dado real usado"
    }
    finally {
      try { Remove-Database $AdminDsn $probeDatabase } catch { Write-Warning "não foi possível remover o banco temporário" }
      if (Test-Path -LiteralPath $probeFile) { Remove-Item -LiteralPath $probeFile -Force }
    }
  }
}
