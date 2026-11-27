# Homologação das funcionalidades existentes — 2026-11-27

Decisão do produto (Ronaldo, 2026-11-27): **OIDC/MFA reais e emissão real de
NFS-e ficam adiados**. Esta rodada homologa o que já existe, em ambiente
descartável com dados sintéticos. Nada foi aplicado no PostgreSQL local
compartilhado (porta 5432); a migration da #82 não foi tocada.

## Escopo e classificação das evidências

| Camada | Resultado | Natureza |
|---|---|---|
| Go: migrations da célula 0001–0039 e do control plane 0001–0004, do zero | PASS | **Real** (PostgreSQL 16 descartável) |
| Go: `go build`, `go vet`, `go test ./...` com DSNs de integração | 20 pacotes `ok`; 366 testes PASS, 0 FAIL, 0 SKIP | **Real** (PG, papéis `NOSUPERUSER NOBYPASSRLS`, RLS efetiva) para repositórios e HTTP via `httptest` |
| Painel da loja (#79): 10.007 títulos/3.037 agendamentos, p95 pág. de títulos 25,9 ms, agenda 39,6 ms | PASS | **Real** em PG descartável; latência de máquina de desenvolvimento, não SLO |
| `/readyz` p95 (200 amostras) | 0,698 ms | Local, sem dependências externas |
| Processo `cmd/api` real (dev) | `/healthz` 200; `/readyz` 503 sem Redis/RabbitMQ/storage (fail-closed esperado); `POST /_dev/onboarding` 200 | **Real** |
| Staging público `hml.amparius.com` | `/`, `/healthz`, `/readyz` 200; readyz com object storage, PostgreSQL, RabbitMQ e Redis `ok` | **Real**, somente leitura |
| Relay Rust: `cargo test` (47 unit + 1 jornada offline + 4 fila), `clippy -D warnings`, `fmt --check` | PASS | Testes in-process; **não** exercitam a API Go real |
| Cliente Rust: `cargo test` | 53 PASS | Testes in-process |
| Web (staging + control plane): `node --test` | 62 PASS | **Simulado** (fakes de `fetch`, sem navegador) |
| NFS-e: motor de tributos, XSD/DPS, adapter | PASS | **Simulado/golden**: adapter fake, sem transmissão; sem `tax_rule` oficial |
| OIDC/MFA | Não homologado | Adiado; autenticadores nos testes são fakes |

## Falhas encontradas e corrigidas

1. **`fiscal.tax_rule` sem GRANT para `amparius_app`** (defeito real). RLS e
   policies existiam desde a 0004, mas nenhuma migration concedia privilégio de
   tabela. Em banco migrado do zero, `fiscal.Checker.LookupTaxRule` e o motor
   de tributos falham com `permission denied for table tax_rule` (fail-closed,
   porém inoperante). O PostgreSQL compartilhado só funcionava por GRANT manual
   não versionado — deriva ambiente × migrations. Correção:
   `migrations/0039_fiscal_tax_rule_grants.sql` (`SELECT, INSERT`; sem
   `UPDATE/DELETE`, pois a regra é datada e a correção vira nova linha).
   Bônus: o staging e qualquer banco novo passam a ter o mesmo comportamento
   dos testes.
2. **Cleanup que nunca rodava** em
   `TestListForCompanyScopesByTenantAndIncludesCoreModules`: `defer
   database.Close()` executa antes de `t.Cleanup`, então o cleanup falhava com
   `closed pool` e deixava licenças e cotas residuais no banco. Corrigido com
   `t.Cleanup(database.Close)` registrado primeiro.
3. **Harness não reproduzível**: `docs/ENGINEERING.md` promete "o papel pode ser
   preparado com:" e o bloco SQL não existe; os testes exigem DML do papel de
   aplicação em `identity.*` e `entitlement.*` que as migrations (corretamente)
   não concedem. Correção: `scripts/homolog-integration.ps1` sobe um container
   descartável (porta 55432), concede esses grants **só nele**, cria os bancos
   da #79 e roda `go test ./...`.

## Observações (sem correção nesta rodada)

- Em `APP_ENV=development` sem OIDC, `/v1/*` e `/admin/v1/*` respondem `404`
  (rotas não montadas); a documentação do #80 fala em `503` — alinhar quando o
  adaptador OIDC/MFA existir.
- `scripts/smoke-readiness.ps1` e `scripts/backup-restore.ps1` apontam para o
  PostgreSQL compartilhado (5432); não foram executados nesta rodada.
- Redis/RabbitMQ locais estavam parados; o `readyz` completo foi verificado
  apenas no staging.
- Não validados: UI no navegador (visual, teclado, WCAG), restore de backup,
  relay contra a API Go real, emissão e consulta reais na Sefin Nacional.

## Reprodução

```powershell
.\scripts\homolog-integration.ps1          # container descartável, remove ao fim
cargo test --manifest-path relay\Cargo.toml
cargo test --manifest-path client\Cargo.toml
node --test web/control-plane/tests/contract.test.mjs web/control-plane/tests/staging-demo.test.mjs web/staging/client-api.test.mjs
```
