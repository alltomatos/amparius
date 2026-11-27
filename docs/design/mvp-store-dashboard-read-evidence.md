# #79 — Leitura operacional da Loja 01/04

Fatia de backend de [E2E-01B8](https://github.com/alltomatos/amparius/issues/79)
na base `957fb27`. Referências de produto: [matriz dos mockups](mvp-mockup-gap-matrix.md),
[prévia Loja 01/04](../../web/staging/index.html) e [PRD J3](../prd/PRD.md#j3--fechamento-mensal).

- Loja 01 consome a [agenda autenticada](../contracts/funeral-schedule-http-v1.md#agenda-da-empresa).
  A leitura da janela civil usa o índice existente da agenda e filtra tenant
  e empresa. A projeção de confirmação deriva de `scheduled` no mesmo
  resultado. O entitlement por empresa usa o checker atual do control plane.
- Loja 04 consome a [listagem financeira](../contracts/finance-titles-payments-v1.md).
  A tabela de títulos tem RLS por tenant e índice em `(tenant_id, due_date, id)`.
  A identidade valida a empresa do principal durante a autenticação. A
  coleção de títulos permanece do grupo econômico, conforme o contrato
  anterior. Isolamento financeiro por empresa exige vínculo persistido, regra
  de atribuição dos dados legados e autorização em todos os fluxos financeiros.

O schema da célula permanece na versão 0038. As consultas usam os schemas dos
respectivos módulos. Os logs HTTP existentes registram resultado, duração e
correlação. A agenda projeta referências operacionais e horários.

## Reprodução em PowerShell

Execute na raiz da worktree, com Docker, Goose e Go instalados. Aguarde o
PostgreSQL aceitar conexões antes de criar os papéis. Os dois bancos são
descartáveis, usam fixtures sintéticas e ficam no container publicado em
`127.0.0.1` com porta dinâmica.

```powershell
docker run --rm --name amparius-issue79-pg16 -d -p 127.0.0.1::5432 -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=issue79_cell postgres:16-alpine
docker exec amparius-issue79-pg16 pg_isready -h 127.0.0.1 -U postgres -d issue79_cell
docker exec amparius-issue79-pg16 psql -U postgres -d issue79_cell -v ON_ERROR_STOP=1 -c "SHOW server_version;" -c "CREATE ROLE amparius_app LOGIN NOSUPERUSER NOBYPASSRLS;" -c "CREATE ROLE amparius_control_app LOGIN NOSUPERUSER NOBYPASSRLS;" -c "CREATE DATABASE issue79_control;"
$port = (docker port amparius-issue79-pg16 5432/tcp).Split(':')[-1]
$env:AMPA_ISSUE79_CELL_ADMIN_DSN = "postgres://postgres@127.0.0.1:$port/issue79_cell?sslmode=disable"
$env:AMPA_ISSUE79_CONTROL_ADMIN_DSN = "postgres://postgres@127.0.0.1:$port/issue79_control?sslmode=disable"
goose -dir migrations postgres $env:AMPA_ISSUE79_CELL_ADMIN_DSN up-to 38
goose -dir migrations/control postgres $env:AMPA_ISSUE79_CONTROL_ADMIN_DSN up-to 3
$env:AMPA_TEST_POSTGRES_DSN = "postgres://amparius_app@127.0.0.1:$port/issue79_cell?sslmode=disable"
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = $env:AMPA_ISSUE79_CELL_ADMIN_DSN
go test -p 1 -count=1 -v ./cmd/api ./internal/modules/finance ./internal/modules/funeral -run 'TestStoreDashboardHTTPPostgres|TestAgendaCivilWindow|TestFinanceProductionHTTPPostgres|TestFuneralJourneyProductionHTTPPostgres|TestBuildHandlerSobreviveCelulaIndisponivel'
docker stop amparius-issue79-pg16
```

## Resultado em 26/11/2026

PostgreSQL 16.16, célula 0038 e control plane 0003. RLS efetiva em
`finance.title`, `funeral.schedule` e `entitlement.license_assignment`,
com `NOSUPERUSER NOBYPASSRLS`. Os testes passaram com validação de vínculo
da identidade no banco, autorização, isolamento entre tenants e entre duas
empresas do mesmo tenant na agenda, licença ausente e indisponível, janelas
civis de 23 e 25 horas, listas vazias, filtros e paginação.

```text
PASS TestBuildHandlerSobreviveCelulaIndisponivelSemDevESemControlPlane
PASS TestStoreDashboardHTTPPostgres
PASS TestFinanceProductionHTTPPostgres
PASS TestAgendaCivilWindow
PASS TestFuneralJourneyProductionHTTPPostgres
```

| Consulta | Massa total | Carga sequencial | p50 | p95 | Máximo |
| --- | ---: | --- | ---: | ---: | ---: |
| Títulos, página 2 com 50 itens | 10.007 títulos, dois tenants | 100 requisições após 10 aquecimentos | 19,40 ms | 21,13 ms | 23,52 ms |
| Agenda diária, 33 itens | 3.037 agendamentos, três empresas | 100 requisições após 10 aquecimentos | 31,63 ms | 34,91 ms | 44,03 ms |

O teste mede o caminho HTTP em processo com banco real, resolução de
identidade e RBAC. OIDC e MFA são simulados. O p95 de ambos ficou abaixo
do orçamento local de 100 ms. O ambiente não mede rede externa, concorrência
de produção ou latência da hospedagem. A suíte `go test -p 1 -count=1 ./...`
passou em execução separada sem DSNs. O comando focado executou as
integrações listadas acima. O container foi removido.

### Execução adicional do Líder Amparius em 26/11/2026

O Líder Amparius registrou uma execução adicional em PostgreSQL descartável
16.16, com célula 0038 e control plane 0003. Os papéis de aplicação tinham
`NOSUPERUSER NOBYPASSRLS`, com RLS efetiva. Os cinco testes de nível superior
listados acima passaram. O p95 foi de **24,041 ms para títulos** e **40,802 ms
para agenda**, dentro do orçamento local de 100 ms. O container foi removido.
O Líder também registrou que passaram o build, o vet, a suíte Go sem DSNs
e 51 testes Node.

A execução integrou o caminho HTTP com autenticação de teste. O bootstrap
com OIDC/MFA real e a integração das telas privadas seguem pendentes.
A prévia pública usa dados sintéticos. Veja o [procedimento no guia de
staging](../userguide/operacao-staging.md#loja-01-04-leituras-autenticadas).

## Critérios restantes da #79

1. Conectar Loja 01/04 ao contexto autenticado real, com OIDC/MFA no
   bootstrap, fuso da loja definido pela configuração de produto, estados de
   carregamento, vazio, erro e módulo bloqueado, paginação e indicação do escopo
   financeiro do grupo. Exibir as pendências operacionais online e o estado da
   fila local de acordo com o CL-01.
2. Ligar recebimentos, parcelas e saldo real à Loja 04. Completar abertura,
   movimentação e fechamento do caixa com idempotência e conciliar baixas,
   caixa e extrato bancário, registrando divergências.
3. Completar o J3 com consolidação de receitas, impostos por documento,
   pendências, inadimplência e margem. Permitir ao contador exportar XML,
   apuração e arquivo contábil com autorização e validação de ponta a ponta.
