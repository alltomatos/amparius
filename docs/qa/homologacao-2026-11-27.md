# Homologação das funcionalidades existentes — 2026-11-27

Decisão do produto (Ronaldo, 2026-11-27): **OIDC/MFA reais e emissão real de
NFS-e ficam adiados**. Esta rodada homologa o que já existe, em ambiente
descartável com dados sintéticos. Nada foi aplicado no PostgreSQL local
compartilhado (porta 5432); a migration da #82 não foi tocada.

## Escopo e classificação das evidências

| Camada | Resultado | Natureza |
|---|---|---|
| Go: migrations da célula 0001–0039 e do control plane 0001–0004, do zero | PASS | **Real** (PostgreSQL 16 descartável) |
| Go: `go build`, `go vet`, `go test ./...` com DSNs de integração | 20 pacotes `ok`; 367 testes PASS, 0 FAIL, 0 SKIP | **Real** (PG, papéis `NOSUPERUSER NOBYPASSRLS`, RLS efetiva) para repositórios e HTTP via `httptest` |
| Painel da loja (#79): 10.007 títulos/3.037 agendamentos, p95 pág. de títulos 25,9 ms, agenda 39,6 ms | PASS | **Real** em PG descartável; latência de máquina de desenvolvimento, não SLO |
| `/readyz` p95 (200 amostras) | 0,698 ms | Local, sem dependências externas |
| Processo `cmd/api` real (dev) | `/healthz` 200; `/readyz` 503 sem Redis/RabbitMQ/storage (fail-closed esperado); `POST /_dev/onboarding` 200 | **Real** |
| Staging público `hml.amparius.com` | `/`, `/healthz`, `/readyz` 200; readyz com object storage, PostgreSQL, RabbitMQ e Redis `ok` | **Real**, somente leitura |
| Relay Rust: `cargo test` (47 unit + 1 jornada offline + 4 fila), `clippy -D warnings`, `fmt --check` | PASS | Testes in-process; **não** exercitam a API Go real |
| Cliente Rust: `cargo test` | 53 PASS | Testes in-process |
| Web (staging + control plane): `node --test` | 62 PASS | **Simulado** (fakes de `fetch`, sem navegador) |
| NFS-e: motor de tributos, XSD/DPS, adapter | PASS | **Simulado/golden**: adapter fake, sem transmissão; sem `tax_rule` oficial |
| Backup/restore (`scripts/homolog-backup-restore.ps1`): `pg_dump -Fc` da célula migrada (0039) + `pg_restore` em outro banco | PASS: versão goose, 66 tabelas, 54 com RLS, 57 policies, 75 triggers, 219 índices, 112 grants do app (hash idêntico) e linhas idênticas; RLS efetiva no restaurado (dono vê 1, tenant alheio 0) | **Real** (PG descartável, dados sintéticos) |
| Readiness completa (`scripts/homolog-readiness.ps1`): PG, Redis, RabbitMQ reais + S3 **simulado** (adobe/s3mock) | PASS: sem dependências `/readyz` 503; com todas `/readyz` 200 | Real para PG/Redis/RabbitMQ; **simulado** para object storage (não é MinIO/R2) |
| Relay Rust como processo real (`amparius-relay.exe`, bind 127.0.0.1:18091) | `/healthz` 200; `/relay/v1/status` sem credencial 401 e com cabeçalhos de credencial não emitida 401; rota inexistente 404 | **Real**, só a borda HTTP local. **Não existe integração relay → API Go** (o relay não tem cliente HTTP; o log informa que o PostgreSQL é placeholder). Sync contra a API real **não é homologável** hoje |
| UI web no navegador (staging `hml.amparius.com`, somente leitura, dados sintéticos) | 10 destinos navegáveis sem erros de script, foco movido ao `h1` a cada rota, `aria-current` correto, skip link funcional, 0 imagens sem `alt`, 0 campos sem rótulo, 0 ids duplicados, contraste AA nos 19 textos visíveis do painel, sem overflow horizontal, regiões `status` para mensagens, `prefers-color-scheme` e breakpoints (760/520 px); cadastro de exemplo e filtro vazio ("Nenhum cadastro encontrado.") funcionam | **Real** no navegador, mas o conteúdo é **simulado** (fila "reiniciada ao recarregar"); sem leitor de tela, sem teste de viewport móvel real e sem Lighthouse/axe completo |
| Painel do control plane no navegador (`web/control-plane` servido estático, sem API) | Carrega sem erro de script; `lang=pt-BR`; skip link; 4 destinos de navegação; 0 campos sem rótulo, 0 imagens sem `alt`, 0 ids duplicados, 0 âncoras quebradas; sem API os pedidos `/v1/context` e `/v1/companies` retornam 404 e a tela mostra estados de falha ("Contexto indisponivel", "HTTP_404: Falha HTTP 404") em regiões `status`/`polite` e mensagens de seleção nas seções dependentes | **Real** no navegador, mas **sem backend**: conceder/revogar licença não foi exercitado nesta primeira passada (exercitado na rodada 2, abaixo, com API real e identidade de teste); a mensagem de falha expõe o código técnico ao operador |
| OIDC/MFA | Não homologado | Adiado; autenticadores nos testes são fakes |

## Control plane: API real com identidade de teste (rodada 2)

Identidade de teste injetada só em `_test.go` (`handlerOption`); o binário de produção continua em 503. PostgreSQL descartável (portas 55432/55435), papéis `NOSUPERUSER NOBYPASSRLS`, dados sintéticos de 2 tenants.

| Fluxo | Resultado | Natureza |
|---|---|---|
| API `/v1` e `/admin/v1` com identidade de teste (contexto, empresas, módulos, conceder, revogar, cota com `quotaVersion`, validade, auditoria, 401/403/409, isolamento entre tenants) | PASS (`TestControlPlaneHTTPComIdentidadeDeTeste`) | **Real** (PG + RLS), identidade é **fake** |
| Sem DSN do control plane → `404`; com DSN e sem autenticador → `503` | PASS (2 testes) | **Real** |
| UI `web/control-plane` no navegador contra a API real: contexto, lista de empresas (RLS), 14 módulos, conceder → revogar → reconceder → ajustar cota → ajustar validade, detalhe com tipo/cota/uso/versão relidos da API, auditoria com 7+ eventos, sem erro de script | PASS após correções abaixo | **Real** (navegador + API + PG), identidade fake |

Defeitos achados na UI e corrigidos:

- a) Ajustar cota pela UI retornava `409 STALE_VERSION`: a UI enviava a versão da licença, mas a cota tem versão própria (`entitlement.license_pool.version`) que o detalhe não expunha. Correção: `quotaVersion` em `GET .../entitlements/{module}` e uso na UI (contrato cp06 atualizado; teste verifica `quotaVersion`).
- b) Após PUT/PATCH a UI substituía o detalhe pela resposta da mutação, que não traz tipo, cota nem uso ("Tipo: nao informado", "sem quota configurada"). Correção: relê lista e detalhe da API após cada mutação.
- c) "Atualizado em" mostrava valor inválido quando `updatedAt` é nulo → "nao informado".

Limites desta rodada: identidade de teste não é OIDC/MFA (não homologados); `/admin/v1` só é exercitado por testes Go (não há UI de plataforma consumindo-o); **relay → API continua sem integração real**, portanto não homologado; sem leitor de tela nem mobile real.

Reprodução: `.\scripts\homolog-control-plane-ui.ps1` (testes + remoção do container) ou com `-Serve` para abrir a UI em `http://127.0.0.1:18095/web/control-plane/index.html` (encerra criando `%TEMP%mparius-homolog-ui.stop`).

## Falhas encontradas e corrigidas

1. **`fiscal.tax_rule` sem GRANT para `amparius_app`** (defeito real). RLS e
   policies existiam desde a 0004, mas nenhuma migration concedia privilégio de
   tabela. Em banco migrado do zero, `fiscal.Checker.LookupTaxRule` e o motor
   de tributos falham com `permission denied for table tax_rule` (fail-closed,
   porém inoperante). O PostgreSQL compartilhado só funcionava por GRANT manual
   não versionado — deriva ambiente × migrations. Correção:
   `migrations/0039_fiscal_tax_rule_grants.sql` (`SELECT, INSERT`; sem
   `UPDATE/DELETE`, pois a regra é datada e a correção vira nova linha).
   Prova de escopo: `TestTaxRuleAppRoleGrantScopeAndRLS` confirma privilégios exatos (SELECT/INSERT sim; UPDATE/DELETE não), recusa (42501) INSERT de regra global e de regra de outro tenant, e mostra que o tenant B não enxerga a regra do A. Bônus: o staging e qualquer banco novo passam a ter o mesmo comportamento
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

4. **`scripts/backup-restore.ps1` descartava os GRANTs** (defeito real). O dump e o restore usavam `--no-acl`; medido em PG descartável, o app tinha 112 grants na origem e **0** no banco restaurado, ou seja, um restore deixaria a aplicação sem acesso a nenhuma tabela. Corrigido removendo `--no-acl` do `backup` e do `restore` (o papel `amparius_app` precisa existir no destino; senão o `pg_restore` falha de forma explícita). O modo `test` (tabela sintética) não depende de ACL e ficou como estava. Os scripts `backup-restore.ps1` e `smoke-readiness.ps1` precisam de ferramentas `psql/pg_dump/pg_restore` no host, que a máquina de desenvolvimento não tem; por isso a homologação usa `homolog-backup-restore.ps1`, que roda o cliente PostgreSQL dentro do container.
5. **`scripts/smoke-readiness.ps1` fixava o banco compartilhado (5432)**. Ganhou o parâmetro `-PostgresDsn` (padrão inalterado) para rodar contra o banco descartável.

## Critérios de aceite das issues #79 e #80 (conferência)

**Ambas permanecem abertas**: o merge das fatias não cumpre todos os critérios.

| Issue | Critério | Situação | Evidência |
|---|---|---|---|
| #79 | Agenda e pendências derivadas do contexto validado | Atendido (agenda); pendências = "Nenhuma operação registrada" na prévia | `TestStoreDashboardHTTPPostgres`, `TestAgendaCivilWindow` (PG real) |
| #79 | Títulos com filtros e paginação; estados de vazio e erro | Atendido | `TestFinanceProductionHTTPPostgres`; UI "Nenhum cadastro encontrado."/indisponível |
| #79 | Testes de autorização e latência documentados | Atendido | p95 25,9 ms (títulos) e 39,6 ms (agenda) nesta rodada, 10.007/3.037 registros |
| #79 | Caixa e recebimentos com reconciliação | **Pendente** | matriz de mockups: "Baixas, caixa e indicadores calculados por projeção seguem em fatias próprias" |
| #79 | Acompanhar fechamento e exportação (PRD J3) | **Pendente** | "Menu Relatórios · J3: Ausente" |
| #80 | Listar contexto e empresas autorizadas | **Parcial** | leitura por tenant e licenças existe; diretório de tenants/empresas depende de `ADM-02B` |
| #80 | Consultar cotas/licenças; conceder e revogar com versão; eventos; recusa fora do escopo | Atendido com identidade de teste | `TestAdminPlatformCrossTenantReadsAndWritesIntegration`, `TestPlatformViewsAreReadOnlyForAppRole`, `admin_http_test.go` (409 `STALE_VERSION`, 401/403) |
| #80 | Validar isolamento antes de conectar a superfície pública | Atendido em teste; **superfície não conectada** (`/admin/v1` não montado sem OIDC/MFA, adiado) | rotas respondem 404 em dev |

## Observações (sem correção nesta rodada)

- **404 × 503 resolvido (comportamento intencional, agora documentado e provado por teste).** `404` = rota não montada (sem `AMPA_CONTROL_POSTGRES_DSN` ou em `APP_ENV=development`); `503 AUTHENTICATION_UNAVAILABLE` = rota montada sem autenticador OIDC/MFA (fail-closed); `401` = bearer inválido. A divergência estava só na documentação do #80. Matriz em `docs/ENGINEERING.md` (control plane).
- O S3 do `readyz` local é um simulador (`adobe/s3mock`); a checagem contra object storage real foi vista apenas no staging. As imagens `minio/*` não estão mais no Docker Hub.
- `rabbitmq-diagnostics` via `docker exec` durante a subida derruba o container do RabbitMQ; o script espera pelo log `Server startup complete`.
- UI: o `document.title` não muda por rota (WCAG 2.4.2, baixo impacto); o skip link deixa `#screen-title` na URL (a rota cai no Painel ao recarregar, comportamento aceitável); não há `prefers-reduced-motion` explícito no CSS. Sem correção nesta rodada.
- Não validados: leitor de tela e dispositivos móveis reais, relay contra a API Go real (inexistente), emissão e consulta reais na Sefin Nacional.

## Reprodução

```powershell
.\scripts\homolog-integration.ps1          # container descartável, remove ao fim
.\scripts\homolog-backup-restore.ps1       # backup/restore real em container descartável
.\scripts\homolog-readiness.ps1           # PG/Redis/RabbitMQ reais + S3 simulado
cargo test --manifest-path relay\Cargo.toml
cargo test --manifest-path client\Cargo.toml
node --test web/control-plane/tests/contract.test.mjs web/control-plane/tests/staging-demo.test.mjs web/staging/client-api.test.mjs
```
