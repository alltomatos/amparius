# Operação de staging

Este guia cobre a verificação de uma célula de staging com dados sintéticos.
Não use os comandos de teste contra produção.

## Saúde da API

- `/healthz` responde se o processo está vivo.
- `/readyz` responde `200` somente quando PostgreSQL, Redis, RabbitMQ e o
  object storage S3-compatible respondem; responde `503` se algum probe falhar.
- Se `AMPA_CONTROL_POSTGRES_DSN` estiver configurado, o banco do control plane
  também é obrigatório.

O object storage é configurado por `OBJECT_STORAGE_ENDPOINT`,
`OBJECT_STORAGE_REGION`, `OBJECT_STORAGE_BUCKET`,
`OBJECT_STORAGE_ACCESS_KEY` e `OBJECT_STORAGE_SECRET_KEY`. Para Cloudflare R2,
use a URL da conta, região `auto` e `OBJECT_STORAGE_USE_PATH_STYLE=false`.
Nenhuma chave deve ser colocada no repositório.

O Compose local não inclui servidor de object storage. Para desenvolvimento sem um bucket
remoto, use qualquer endpoint S3-compatible local e configure
`OBJECT_STORAGE_USE_PATH_STYLE=true`; sem essa configuração, a API ainda sobe,
mas permanece `not_ready` de forma explícita.

O corpo de readiness informa apenas `status`, `latency_ms` e o estado de cada
check. DSNs, senhas e erros crus nunca aparecem na resposta.

## Backup e restore de teste

1. Configure `AMPA_BACKUP_TEST_ADMIN_DSN` para uma conexão administrativa de
   um ambiente de teste.
2. Execute:

   ```powershell
   .\scripts\backup-restore.ps1 test
   ```

3. Confirme a mensagem `BACKUP/RESTORE TEST PASS`.

O comando cria e remove um banco temporário e usa somente o marcador
`synthetic-only`. Ausência de `pg_dump`, `pg_restore` ou `psql` é erro; não há
fallback que simule o restore.

Para gerar um backup autorizado:

```powershell
$env:AMPA_BACKUP_DSN = 'postgres://<admin>:<secret>@host:5432/amparius?sslmode=disable'
$env:AMPA_BACKUP_FILE = 'D:\backups\amparius.dump'
.\scripts\backup-restore.ps1 backup
```

Para restaurar em um banco existente, o operador precisa usar
`-AllowDestructiveRestore` conscientemente. O backup deve ser preservado e
verificado antes da operação.

## Rollback da API

Antes do rollback, gere um backup e execute o modo `plan`:

```powershell
.\scripts\rollback-staging.ps1 plan -PreviousImage amparius-api:previous -BackupFile D:\backups\staging.dump
```

Depois, com o backup existente e a imagem anterior disponível:

```powershell
.\scripts\rollback-staging.ps1 apply -PreviousImage amparius-api:previous -BackupFile D:\backups\staging.dump -AllowRollback
```

O rollback troca somente a imagem da API. Ele não executa `goose down`, não
apaga volume e não desfaz migration aplicada. Após o serviço subir, verifique
`/healthz` e `/readyz` uma vez e registre o resultado. Se o schema não for
compatível, interrompa o tráfego e siga o procedimento de restore aprovado.

## Evidência e limites

O p95 reproduzível é executado por:

```powershell
.\scripts\benchmark.ps1 -Iterations 1000
```

Essa medição usa uma dependência sintética e representa somente o caminho HTTP
do probe. O smoke com Compose valida a topologia local; o object storage é
validado pelo teste S3 sintético ou por R2 autorizado. O teste de
backup não prova PITR/WAL contínuo, criptografia do repositório, restore de
object storage ou recuperação de produção.

O probe S3 sintético pode ser repetido sem credenciais:

```powershell
go test ./internal/platform/readiness -run 'TestS3ChecksHeadBucketAgainstGenericEndpoint|TestS3UnconfiguredFailsClosed' -count=1 -v
```
