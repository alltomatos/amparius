# FIS-01B - consulta fiscal fail-closed

Data: 2026-11-24
Issue: #23 (parent #4 MVP-FIS-01)
Estado: implementado, sem commit/push nesta etapa.

## Objetivo

Bloquear a emissao quando a configuracao fiscal ou o certificado do CNPJ do
fato gerador nao estiverem vigentes, sem inventar regra tributaria, sem vazar
segredo/PII e sem permitir leitura cruzada entre tenant e empresa.

## Contrato entregue

`internal/modules/fiscal/emission_gate.go`:

- `EmissionGate.Resolve(ctx, EmissionCriteria)` devolve `EmissionSetup`
  (configuracao + referencia segura do certificado) ou falha fechado.
- Tenant e empresa vem de `db.TenantFromContext` e `db.CompanyFromContext`; o
  corpo HTTP nao define escopo.
- A configuracao e avaliada pela competencia em intervalo half-open. A
  configuracao especifica do estabelecimento tem precedencia sobre a padrao da
  empresa; se nenhuma das duas estiver vigente, bloqueia.
- Estabelecimento informado precisa pertencer ao tenant e a empresa do
  contexto (`ErrEstablishmentNotInScope`).
- Certificado precisa estar `active`, dentro da vigencia no momento de emissao
  (`IssuedAt`, ou a competencia quando zero), com `storage_kind` e `storage_ref`
  preenchidos.
- O contrato nao possui campo de senha, PIN, chave privada, PFX, token ou bytes
  criptograficos; erros usam sentinelas de texto fixo e nao incluem serial,
  fingerprint, `subject_cn` nem `storage_ref`.

Codigos de contrato (`EmissionErrorCode`):

| Situacao | Erro | Codigo | HTTP |
| --- | --- | --- | --- |
| Configuracao ausente | `ErrFiscalConfigMissing` | `FISCAL_CONFIG_MISSING` | 422 |
| Configuracao expirada/inativa/ambigua/malformada | erros `ErrFiscalConfig*` | `FISCAL_CONFIG_INVALID` | 422 |
| Competencia invalida | `ErrInvalidCompetence` | `FISCAL_CONFIG_INVALID` | 422 |
| Estabelecimento fora do escopo | `ErrEstablishmentNotInScope` | `FISCAL_CONFIG_INVALID` | 422 |
| Certificado ausente/expirado/inativo/ambiguo/malformado | erros `ErrFiscalCertificate*` | `FISCAL_CERTIFICATE_INVALID` | 422 |
| Falha de banco nao classificada | erro propagado | vazio | 503 (borda) |

## Banco

Migration forward-only `migrations/0021_fiscal_certificate_company_rls.sql`:

- concede somente `SELECT` em `fiscal.certificate` ao `amparius_app` (cadastro
  e rotacao continuam fora da aplicacao);
- troca a RLS de tenant-only para tenant + empresa, igual a
  `fiscal.fiscal_config` (migration 0020). Sem `app.company_id`, nenhum
  certificado e visivel.

Nenhuma tabela, coluna ou dado tributario novo. Nenhuma aliquota, CST,
cClassTrib, NCM, CFOP ou codigo de servico e semeado.

## Testes e evidencia

Unitarios (`package fiscal`, sem banco):

```powershell
go test ./internal/modules/fiscal -run 'TestEmissionGate|TestEmissionErrorCode' -count=1 -v
```

Cobrem: vigente, precedencia do estabelecimento, expiracao/inatividade/
malformacao/ambiguidade/ausencia de configuracao, certificado ausente/expirado/
inativo/malformado, `IssuedAt` posterior, estabelecimento fora do escopo,
competencia zero, tenant/empresa ausentes, falha de banco e nao vazamento de
metadados nos erros.

Integracao PostgreSQL real (`amparius_app` `NOSUPERUSER NOBYPASSRLS`, cleanup
administrativo conferido):

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:...@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:...@127.0.0.1:5432/amparius?sslmode=disable'
go test ./internal/modules/fiscal -run 'TestEmissionGatePostgresFailClosedValidityCrossTenantAndCleanup' -v -count=1
```

Resultado local desta fatia:

- `go build ./...` OK;
- `go vet ./...` OK;
- `go test ./... -count=1` OK (todos os pacotes, com os dois DSNs acima);
- `goose up` aplicou `0021` (versao 21);
- teste de integracao PASS, incluindo cleanup com `fiscal_config=0` e
  `fiscal_certificate=0` para os tenants de teste.

## Fora do escopo

- Emissao NFS-e/NF-e propriamente dita e transmissao (fatias seguintes).
- Regras de aliquota e codigos fiscais oficiais (tabelas datadas).
- Cache/relay de configuracao fiscal.
- Rotacao e cadastro de certificado no cofre.
- Commit/push: conforme instrucao desta etapa, as mudancas ficam apenas no
  working tree.
