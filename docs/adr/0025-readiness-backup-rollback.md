# ADR 0025 — Readiness real, backup verificável e rollback seguro

Status: aceito · 25/11/2026 · HARDEN-01C (#57)

## Contexto

O endpoint `/readyz` era um stub que respondia sucesso sem verificar a pilha da
célula. Isso permitia que o orquestrador enviasse tráfego para uma API sem
PostgreSQL, Redis, RabbitMQ ou object storage. A arquitetura também tinha a
meta RPO de 5 minutos e RTO de 1 hora, mas não possuía um procedimento
reproduzível de backup/restore nem uma regra executável de rollback.

## Decisão

1. `/healthz` continua sendo liveness: responde 200 somente porque o processo
   está vivo.
2. `/readyz` executa probes reais e retorna 200 apenas quando todas as
   dependências configuradas respondem:
   - PostgreSQL: `Ping` pelo protocolo PostgreSQL;
   - Redis: comando `PING` e resposta `PONG`;
   - RabbitMQ: início do handshake AMQP;
   - object storage: `HeadBucket` assinado pelo protocolo S3;
   - PostgreSQL do control plane, quando `AMPA_CONTROL_POSTGRES_DSN` estiver
     configurado.
3. Falha de qualquer probe retorna 503. A resposta contém apenas o nome,
   estado e latência; não contém DSN, usuário, senha ou erro bruto. O limite
   total é `READINESS_TIMEOUT` (2 s por padrão).
   Object storage usa `OBJECT_STORAGE_ENDPOINT`, `OBJECT_STORAGE_REGION`,
   `OBJECT_STORAGE_BUCKET`, `OBJECT_STORAGE_ACCESS_KEY`,
   `OBJECT_STORAGE_SECRET_KEY` e `OBJECT_STORAGE_USE_PATH_STYLE`.
4. O benchmark reproduzível mede o caminho HTTP/readiness com uma dependência
   sintética em 1.000 amostras. O smoke com Docker Compose valida a topologia
   local instalada; o Adapter S3 é validado por endpoint sintético ou R2
   autorizado. Nenhum número sintético é apresentado como p95 de produção.
5. `scripts/backup-restore.ps1` usa `pg_dump` em formato custom e valida o
   catálogo do dump. O modo `test` cria um banco temporário com um marcador
   sintético, destrói/recria o banco, restaura e verifica o marcador, removendo
   tudo ao final. O modo `restore` exige `-AllowDestructiveRestore`.
6. `scripts/rollback-staging.ps1` troca somente a imagem da API, exige backup
   existente e `-AllowRollback`, e nunca executa `goose down` nem apaga
   volumes. Migrações aplicadas continuam forward-only; rollback de schema
   exige uma nova migration compatível ou restore deliberado do banco.

## Consequências

- O balanceador pode retirar a instância da rotação sem confundir processo vivo
  com dependência pronta.
- O startup não faz ping bloqueante dos serviços externos para a API continuar
  expondo diagnóstico; a readiness é a decisão de tráfego.
- O endpoint faz uma consulta de saúde por dependência, portanto seu p95 deve
  ser acompanhado junto da latência das dependências. O timeout limita o custo
  de uma dependência indisponível.
- O teste operacional de backup não prova PITR/WAL contínuo, criptografia do
  repositório, restore de object storage S3, nem dados de produção. Essas provas
  permanecem no runbook da célula.
- Staging sobe somente PostgreSQL, Redis e RabbitMQ. Object storage é uma
  dependência externa S3-compatible, configurada por endpoint, região, bucket
  e credenciais fora do repositório.

## Segurança e LGPD

- DSNs entram somente por variável de ambiente/secret store e nunca são
  impressos pelos scripts ou pela resposta de readiness.
- O teste de restore usa somente `synthetic-only`; não deve apontar para uma
  base de produção.
- Backup de produção deve permanecer criptografado, com controle de acesso e
  retenção definida pela política de LGPD e fiscal.

## Verificação

```powershell
go test ./...
go vet ./...
go build ./...
.\scripts\benchmark.ps1 -Iterations 1000
go test ./internal/platform/readiness -run 'TestS3ChecksHeadBucketAgainstGenericEndpoint|TestS3UnconfiguredFailsClosed' -count=1 -v
$env:AMPA_BACKUP_TEST_ADMIN_DSN = 'postgres://<admin>:<secret>@127.0.0.1:5432/amparius?sslmode=disable'
.\scripts\backup-restore.ps1 test
```

O smoke real de readiness exige `docker compose up -d` e a API configurada;
sem esses serviços o resultado esperado é 503 com os checks que falharam.
