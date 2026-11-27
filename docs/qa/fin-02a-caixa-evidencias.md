# Evidências de FIN-02A, parte 1 (#99)

Validação de 27/11/2026 em `D:\dev\amparius`, branch
`feat/fin-02a-caixa`, HEAD `d8f57a8`. O HEAD descende de `090572b` e
os commits intermediários não alteram o módulo financeiro. A entrega
ficou local, sem stage, commit ou push.

## Arquivos da entrega

| Arquivo | Alteração |
| --- | --- |
| `migrations/0040_finance_cash_session_close.sql` | FK por tenant, unicidade das chaves e da sessão aberta, dados de fechamento e UPDATE por coluna |
| `internal/modules/finance/cash_session.go` | Abertura, paginação, fechamento e conciliação |
| `internal/modules/finance/cash_session_http.go` | Handlers, respostas e validação |
| `internal/modules/finance/production_http.go` | Rotas autenticadas, erros e vínculo opcional na baixa |
| `internal/modules/finance/repository.go` | Busca idempotente antes de validar estado, locks de sessão e gravação de vínculos |
| `internal/modules/finance/cash_session_http_test.go` | Testes HTTP, PostgreSQL, RLS, concorrência e latência |
| `docs/adr/0047-finance-cash-sessions.md` | Decisão sobre armazenamento, locks e permissões |
| `docs/contracts/finance-cash-sessions-v1.md` | Contrato, exemplos, erros e percentis |
| `docs/contracts/finance-titles-payments-v1.md` | `cash_session_id` e erros da baixa vinculada |
| `docs/ENGINEERING.md` | Comando de verificação e documentação da fatia. BOM UTF-8 preservado |

O fechamento guarda chave própria e nota para comparar replays. A ordem
entre fechamento e lançamento é definida pelos locks da sessão. Uma
baixa que já foi gravada pode ser reapresentada após o fechamento.
A validação HTTP rejeita um `cash_session_id` em branco quando enviado
como string, evitando que um vínculo malformado seja omitido em silêncio.
Os testes finais não revelaram defeito fora do escopo.

## Migração descartável

PostgreSQL `16.16`, imagem `postgres:16-alpine`. Use DSNs de um container
isolado, com `amparius_app` criado com `NOSUPERUSER NOBYPASSRLS`. Para
reproduzir o caso legado, execute após `goose up-to 39` e antes de
`goose up`, via conexão administrativa descartável.

```sql
WITH t AS (
    INSERT INTO identity.tenant (name, document, regime, status)
    VALUES ('Legacy 0040', '00000000000100', 'economic_group', 'active')
    RETURNING id
)
INSERT INTO finance.cash_session (tenant_id, opening_amount, status)
SELECT id, 42.50, 'closed' FROM t;
```

```powershell
goose -dir migrations postgres $env:AMPA_TEST_POSTGRES_ADMIN_DSN up
goose -dir migrations postgres $env:AMPA_TEST_POSTGRES_ADMIN_DSN down
goose -dir migrations postgres $env:AMPA_TEST_POSTGRES_ADMIN_DSN up
```

Saída observada.

```text
OK   0040_finance_cash_session_close.sql (69.34ms)
goose: successfully migrated database to version: 40
OK   0040_finance_cash_session_close.sql (31.5ms)
OK   0040_finance_cash_session_close.sql (67.62ms)
goose: successfully migrated database to version: 40
 status | timestamp_backfilled | expected_amount | counted_amount | difference_amount
 closed | t                    |           42.50 |          42.50 |              0.00
```

### Duas sessões abertas legadas para o mesmo tenant e usuário

Reprodução em PowerShell com PostgreSQL 16.16, em um container próprio. Prossiga com o `psql` quando `pg_isready` indicar `accepting connections`.

```powershell
$container = 'amparius-fin02a-review-20261127'
docker run --rm -d --name $container -p 127.0.0.1::5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:16-alpine
docker exec $container pg_isready -U postgres
docker exec $container psql -X -v ON_ERROR_STOP=1 -U postgres -c 'CREATE ROLE amparius_app LOGIN NOSUPERUSER NOBYPASSRLS'
docker exec $container psql -X -v ON_ERROR_STOP=1 -U postgres -c 'CREATE DATABASE migration_failure'
$port = (docker port $container 5432/tcp).Split(':')[-1]
$dsn = "postgres://postgres@127.0.0.1:${port}/migration_failure?sslmode=disable"
if (Test-Path migrations/0036*) { throw 'Unexpected migration 0036' }
goose -dir migrations postgres $dsn up-to 39
@'
WITH t AS (
    INSERT INTO identity.tenant (name, document, regime, status)
    VALUES ('Legacy duplicate 0040', '00000000000100', 'economic_group', 'active')
    RETURNING id
)
INSERT INTO finance.cash_session (tenant_id, user_id, opening_amount, status)
SELECT t.id, '00000000-0000-4000-8000-000000000099', 42.50, 'open'
FROM t CROSS JOIN generate_series(1, 2);
'@ | docker exec -i $container psql -X -v ON_ERROR_STOP=1 -U postgres -d migration_failure
goose -dir migrations postgres $dsn up
Write-Output "goose up exit: $LASTEXITCODE"
goose -dir migrations postgres $dsn version
@'
SELECT count(*) AS legacy_open
FROM finance.cash_session WHERE status = 'open';
SELECT count(*) AS migration_0040_columns
FROM information_schema.columns
WHERE table_schema = 'finance'
  AND ((table_name = 'payment' AND column_name = 'session_id')
    OR (table_name = 'cash_session' AND column_name = 'difference_amount'));
SELECT count(*) AS migration_0040_indexes
FROM pg_indexes
WHERE schemaname = 'finance'
  AND indexname IN ('payment_tenant_session_paid_idx', 'cash_session_tenant_idempotency_uq',
                    'cash_session_tenant_close_key_uq', 'cash_session_tenant_open_user_uq');
'@ | docker exec -i $container psql -X -v ON_ERROR_STOP=1 -U postgres -d migration_failure
docker stop $container
docker ps -a --filter "name=^/$container$" --format '{{.Names}}'
```

Trecho da saída de 27/11/2026. A transação da 0040 foi revertida. As duas sessões permanecem abertas, a versão ficou em 39 e as sondagens das colunas e índices retornaram zero.

```text
2026/11/27 14:06:36 goose: successfully migrated database to version: 39
INSERT 0 2
2026/11/27 14:06:37 goose run: ERROR 0040_finance_cash_session_close.sql: failed to run SQL migration: failed to execute SQL query "CREATE UNIQUE INDEX cash_session_tenant_open_user_uq\n    ON finance.cash_session (tenant_id, user_id) WHERE status = 'open' AND user_id IS NOT NULL;": ERROR: could not create unique index "cash_session_tenant_open_user_uq" (SQLSTATE 23505)
goose up exit: 1
2026/11/27 14:06:37 goose: version 39
 legacy_open
-------------
           2
(1 row)

 migration_0040_columns
------------------------
                      0
(1 row)

 migration_0040_indexes
------------------------
                      0
(1 row)
```

O container foi removido após os testes. A lista de containers filtrada pelo nome voltou vazia.

## Testes HTTP e banco real

```powershell
go test ./internal/modules/finance -run '^TestCashSessionHTTP' -v -count=1
```

```text
--- PASS: TestCashSessionHTTPValidation (0.06s)
    RLS direta: sessões, movimentos e baixas do outro tenant invisíveis; UPDATE de abertura e DELETE negados (42501)
    fechamento concorrente: baixa HTTP 409, esperado 10000; diferença persistida -30 centavos no fluxo principal
--- PASS: TestCashSessionHTTPPostgres (0.56s)
PASS
ok      github.com/alltomatos/amparius/internal/modules/finance    0.946s
```

No fluxo principal o esperado foi 14500 centavos, o contado foi 14470
e Pix e cartão ficaram no detalhamento. O teste de concorrência valida
o esperado de acordo com a ordem em que as duas operações terminam.
Na execução registrada, o fechamento venceu a corrida. O teste confirmou
o papel restrito e usou consultas diretas sem filtro de tenant para testar
RLS. O cleanup deixou zero tenants das fixtures.

### Observabilidade do fechamento

```powershell
go test ./internal/modules/finance -run '^TestCashSessionHTTPPostgres$' -v -count=1
```

```text
    cash_session_http_test.go:269: observabilidade: divergência -30 gerou um log estruturado e contador Prometheus 1 após replay, conflitos e fechamento sem diferença
--- PASS: TestCashSessionHTTPPostgres (0.57s)
PASS
ok      github.com/alltomatos/amparius/internal/modules/finance    0.732s
```

O teste conferiu os IDs opacos de tenant, sessão e usuário, o valor em centavos e a ausência da nota no log. O binário da API com `API_METRICS_PORT` configurado também retornou a amostra inicial por HTTP.

```text
GET /metrics on loopback: HTTP 200
# TYPE amparius_finance_cash_session_close_difference_total counter
amparius_finance_cash_session_close_difference_total 0
GET /metrics on application HTTP listener: HTTP 404
```

### Latência

```powershell
$env:AMPA_FIN02A_LATENCY = '1'
go test ./internal/modules/finance -run '^TestCashSessionHTTPPostgres$' -v -count=1
```

```text
latência abrir n=200 p50=10.3469ms p95=13.904ms p99=18.0154ms
latência movimentar n=200 p50=11.8885ms p95=16.0102ms p99=19.1815ms
latência baixar n=200 p50=12.1284ms p95=16.0877ms p99=22.0503ms
latência fechar n=200 p50=11.9126ms p95=16.1088ms p99=19.3056ms
latência conciliar n=200 p50=5.9602ms p95=7.845ms p99=9.8822ms
--- PASS: TestCashSessionHTTPPostgres (12.30s)
PASS
ok      github.com/alltomatos/amparius/internal/modules/finance    12.545s
```

## Gates do repositório

A suíte completa usou bancos descartáveis de célula, control plane e
da #79, com `GOFLAGS=-p=1` para executar os pacotes sequencialmente.
Os comandos terminaram com as saídas abaixo. O PostgreSQL descartável
foi removido ao final.

```text
go build ./... => 0
go vet ./... => 0
ok      github.com/alltomatos/amparius/cmd/api                     8.787s
ok      github.com/alltomatos/amparius/internal/modules/finance    2.457s
go test ./... -count=1 => 0
git diff --check => 0
git diff --no-index --check (arquivos novos) => sem ocorrências
```

Na revisão de 27/11/2026, a primeira execução da suíte retornou `42501` em fixtures antigas. A execução final usou permissões de fixture concedidas apenas nos bancos descartáveis de célula e control plane. Os papéis `amparius_app` e `amparius_control_app` continuaram `NOSUPERUSER NOBYPASSRLS`.

```sql
-- Banco de célula descartável
GRANT INSERT ON identity.tenant, identity.company, identity.establishment TO amparius_app;
-- Banco de control plane descartável
GRANT INSERT, DELETE ON entitlement.module TO amparius_control_app;
GRANT DELETE ON entitlement.license_assignment, entitlement.license_pool, entitlement.user_quota TO amparius_control_app;
GRANT UPDATE, DELETE ON entitlement.entitlement_event, entitlement.quota_event TO amparius_control_app;
```

Saída da revisão com `$env:GOFLAGS = '-p=1'`.

```text
go build ./... exit: 0
go vet ./... exit: 0
ok      github.com/alltomatos/amparius/cmd/api                     8.854s
ok      github.com/alltomatos/amparius/internal/modules/entitlement 3.359s
ok      github.com/alltomatos/amparius/internal/modules/finance    2.559s
ok      github.com/alltomatos/amparius/internal/modules/outbox      4.195s
go test ./... -count=1 exit: 0
git diff --check exit: 0
Whitespace check on new FIN-02A files: 7 clean
git diff --cached --exit-code --quiet exit: 0
```

O teste exercitou o middleware com autenticação sintética. OIDC externo e
latência de produção não foram medidos. OFX/CNAB, papéis e tolerância formal,
offline e Loja 04 pertencem a outras fatias.
