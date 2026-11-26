# Aderência do MVP aos mockups

Levantamento de 26/11/2026, a partir de main 0d22237, dos quatro SVGs da
[loja](../../mockups/index.html), dos sete SVGs da
[administração SaaS](mockups/index.html) e da abertura das superfícies públicas.
As prioridades seguem o [PRD](../prd/PRD.md), o [DER](../data/DER.md), o
[roadmap](../roadmap/ROADMAP.md), a [especificação frontend](frontend-spec.md)
e o [aceite](../qa/frontend-acceptance.md). As áreas operacionais e administrativas
têm domínios separados conforme o [ADR 0040](../adr/0040-plataforma-cross-tenant-manager.md).

**Funcional** identifica uma capacidade exercitada na superfície indicada.
**Parcial** identifica uma implementação sem os componentes necessários para
a jornada. **Simulado** identifica um cenário isolado, com dados sintéticos.
**Ausente** identifica a falta da capacidade na superfície indicada.

Na abertura de https://hml.amparius.com/ havia três âncoras na mesma página,
sem Cadastros. https://manager.amparius.com/ mostrava Painel, Empresas,
Licenças e Auditoria, com contexto fixo. O domínio .com.br serve probes
conforme o [edge](../../deploy/staging/Caddyfile).

Atualização da fatia #72 (26/11/2026), sobre a base b741578 da branch
`feat/mvp-integrated-journey`: o cadastro de pessoa física autenticado
(`POST` e `GET /v1/parties`, [contrato](../contracts/party-pf-v1.md) e
[ADR 0041](../adr/0041-cadastro-pf-autenticado-v1.md)) entrou no backend. A
prévia pública continua sintética e sem essa rota; o bootstrap de produção
continua sem provedor OIDC/MFA e responde `503`.

## Loja

| Referência | Prévia web após esta fatia | Go e Rust | Prioridade e dependência |
| --- | --- | --- | --- |
| Menus Loja 01–04 | **Funcional como navegação:** dez destinos com tela e indicador ativo, incluindo Cadastros e Sincronização. | Links e contexto da [prévia](../../web/staging/index.html). | P0 coberto nesta fatia visual. |
| Loja 01 / CL-01 · Painel do dia | **Simulado.** Agenda e pendência demonstrativa, unidade fixa e estado da fila. KPIs operacionais e resumo mensal ausentes. | **Parcial:** projeção da fila na [UI Rust](../../client/src/ui/day_panel.rs). A API tem agenda por [contrato](../../internal/modules/funeral/schedule_http.go). | P1, projeção de agenda e pendências por contexto após cadastro e jornada. |
| PRD RF-003/004 / Loja 02 / CL-02 · Partes | **Simulado.** Cadastro de exemplos PF, PJ, falecido e vínculo e seleção de responsável. | **Parcial.** PF autenticada na #72. O [contrato da #73](../contracts/party-deceased-family-link-v1.md) cobre falecido e vínculo por API. As duas criações exigem política e gravam auditoria atomicamente. O vínculo registra consentimento com UUID externo e rejeita autovínculo. Duplicidades novas retornam 409 e o legado mantém os IDs. Sem migração nova. | P0. No binário atual, as quatro rotas da #73 retornam `503 AUTHENTICATION_UNAVAILABLE`. Com autenticador e sem política, os POSTs retornam `503 PRIVACY_POLICY_UNAVAILABLE`. Provedor real, política DPO, UI integrada, PJ, contatos, endereços e cache autorizado pendentes. |
| PRD §4.1 / Loja 02 · Catálogo | **Simulado.** Produto ou serviço de exemplo; só serviços podem ser usados no atendimento. Sem preços ou regra fiscal. | **Parcial:** [catálogo operacional v1](../contracts/catalog-operational-v1.md) cadastra serviço, tabela e preço e resolve oferta vigente sob RBAC e RLS. | P0. Consulta por IDs e data civil explícita. Sem OIDC/MFA, responde 503. Descoberta e seleção no cliente na [#75](https://github.com/alltomatos/amparius/issues/75). Consumo pelo contrato autenticado e replay do ID do preço e dos centavos na [#76](https://github.com/alltomatos/amparius/issues/76). |
| Loja 02 / CL-02 · Atendimento e Contratos | **Simulado.** Escolha de responsável/serviço e enfileiramento com contrato ativo de exemplo. Jornada de contrato novo, pagamento e assinatura ausentes. | **Parcial:** [Go funeral](../../internal/modules/funeral/http.go) e [jornada](../../internal/modules/funeral/journey.go) cobrem contrato, OS e agenda em dev. Rust enfileira jornada por referência ativa. | P0, partir dos cadastros reais, montar contrato e confirmar OS/agenda. Implementar pagamento e assinatura em fatias distintas. |
| CL-03 · Ordens e agenda | **Ausente.** Atendimento da prévia só representa o enfileiramento. | **Parcial:** [OS](../../internal/modules/funeral/service_order_http.go), [agenda](../../internal/modules/funeral/schedule_http.go) e UI [Rust](../../client/src/ui/render.rs) de ordens com estado derivado da fila. | P1, após contrato, testar criação, transição e conflito pelo caminho normal. |
| CL-04/05 · Operação offline e sincronização | **Simulado.** Reconexão e reenvio em memória. | **Parcial ponta a ponta:** cliente tem [fila persistida](../../client/src/lib.rs), [transporte](../../client/src/transport.rs), relay tem [protocolo](../../relay/src/http.rs) e [reconciliação](../../relay/src/reconciliation.rs). | P0 para o fluxo funerário e caixa. Provar estação → relay → SaaS, queda real, replay e revisão, conforme [#59](https://github.com/alltomatos/amparius/issues/59). |
| Loja 03 · Cemitério | **Ausente.** Menu indica mapa/concessões indisponíveis. | **Ausente como pacote integrado:** referências no [DER](../data/DER.md); sem módulo Go/cliente de mapa e concessão no recorte inspecionado. | P2, pacote opcional, entitlement e modelo operacional de jazigo/concessão. |
| Loja 04 · Financeiro | **Ausente.** Menu indica dependência de integração. | **Parcial:** [finance dev](../../internal/modules/finance/http.go) tem contas, títulos, parcelas, baixas e caixa. Fila Rust cobre operações locais específicas. | P0, listar títulos em contexto autenticado, depois baixar e fechar caixa com idempotência. |
| Menu Flores · J4 | **Ausente.** Menu indica indisponibilidade. | **Parcial:** itens e preços no catálogo compartilhado. Pedido, estoque e entrega não aparecem como módulo integrado. | P2, entitlement, catálogo e fluxo de pedido. |
| Menu Fiscal · J1/J2 | **Ausente na loja.** Emissão indisponível nesta prévia. | **Parcial:** motor e persistência em [Go](../../internal/modules/fiscal/) e revisão no relay. Gates rastreados em [#25](https://github.com/alltomatos/amparius/issues/25). | P0 espinha fiscal, em trilha própria. Regras oficiais datadas e evidência de emissão para liberar operação real. |
| Menu Relatórios · J3 | **Ausente.** Menu indica dependência de projeções. | **Parcial:** dados financeiros e fiscais persistidos; sem superfície de fechamento/exportação. | P1, após projeções de títulos, caixa e documentos autorizados. |
| Assistente · quatro SVGs de loja | **Ausente.** Painel indica indisponibilidade. | **Parcial:** contratos de [assistente](AI-ASSISTANT.md) e [ADR 0008](../adr/0008-ia-providers-quota.md). | P2, autorização, mascaramento e cota precedem uso. Offline permanece sem LLM. |

## Administração SaaS

| Referência | Manager público | Backend / dependência |
| --- | --- | --- |
| SaaS 03 / CP-03 · Visão geral | **Simulado:** indicadores e uma empresa de demonstração. | **Parcial:** [contexto e empresas](../../internal/modules/controlplane/http.go); falta agregação administrativa real. |
| SaaS 05 / CP-05 · Cotas | **Simulado:** licenças por empresa. Consolidação multiempresa e troca de tenant ausentes. | **Parcial:** [serviço de entitlement](../../internal/modules/entitlement/service.go) e contexto de empresa. |
| SaaS 06 / CP-06 · Licenças | **Simulado:** concessão, revogação, ajustes e versão na aba. | **Parcial:** handlers e rotas autenticáveis em [cmd/api](../../cmd/api/main.go), bootstrap com autenticador indisponível. |
| SaaS 07 / CP-09 · Auditoria | **Simulado:** eventos da licença selecionada. Filtros globais e exportação ausentes. | **Parcial:** leitura de auditoria específica de entitlement. |
| SaaS 08 / CP-07 · Provedores | **Ausente.** | **Parcial:** modelo no DER e ADR 0008; falta ciclo autorizado de provedor/modelo e secret ref. |
| SaaS 09 / CP-07 · Modelos e cota | **Ausente.** | **Parcial:** desenho de allowlist, fallback e teto em BRL; faltam endpoints e consumo da configuração. |
| SaaS 10 / CP-07 · Consumo | **Ausente.** | **Parcial:** modelo de trilha e consumo no DER; faltam agregados e tela autorizada. |
| CP-01/02/04/08 · Acesso, tenant, empresa, usuários | **Ausente** como jornada autenticada. | **Parcial:** middleware RBAC e [ADR 0024](../adr/0024-autenticacao-oidc-mfa-rbac-escopo.md). O bootstrap atual deixa rotas administrativas em falha fechada. |

## Ordem de implementação e critérios

1. **Navegação da loja, entregue no código.** Seções do mockup com destino
   próprio, destaque ativo, navegação por fragmento e estado de estação em
   todas as seções. Rotas pendentes identificam a capacidade indisponível.
2. **Cadastros sintéticos, entregue no código.** Filtrar por nome, criar
   exemplos, vincular familiar sem duplicar e usar responsável e serviço
   cadastrados no atendimento. Confirmar duas vezes mantém uma operação,
   reconexão permite simular o envio e recarregar repõe o cenário inicial.
3. **Cadastros operacionais, próximo P0.** Pessoa física autenticada já está
   no backend (#72), assim como falecido, vínculo e consentimento (#73).
   Próxima fatia funcional: [#74, catálogo operacional, serviço e preço vigente](https://github.com/alltomatos/amparius/issues/74).
   Em ambiente local ou privado,
   autenticar com contexto de tenant e papel, emitir contrato versionado de
   PF/busca/falecido/vínculo e catálogo, testar 401/403, acesso cruzado negado,
   criação/consulta persistida e ausência de PII nos logs. Usar credenciais e
   dados de desenvolvimento isolados para validar antes do provedor de produção.
   As rotas de desenvolvimento confiam em contexto fornecido pelo chamador e
   continuam fora do edge público.
4. **Contrato → OS → agenda → caixa.** Consumir IDs reais selecionados,
   validar licença, status e versão no servidor, fazer replay sem duplicação.
   O fluxo online e offline requer provas distintas antes de fechar #59.
5. **Títulos/caixa e painel real.** Entregar leitura paginada com filtros e
   estados; depois baixas, caixa, agenda do dia e indicadores calculados por
   projeção. Fiscal oficial e pacotes opcionais têm trilhas próprias.

## Evidência e limites

- Navegador local, pacote gerado por deploy/staging/prepare-preview.sh. O
  fluxo PF → falecido → vínculo → busca por nome → atendimento → fila
  preservou uma operação e o responsável selecionado. Cadastro de PJ e
  serviço, busca sem resultados e restauração do cenário também foram
  exercitados. A segunda tentativa
  de vínculo foi recusada. A reconexão simulada permitiu concluir a fila.
- PostgreSQL 16 temporário, migrations da célula até 0035, papel de aplicação
  com rolsuper=false e rolbypassrls=false. Os casos com fixture administrativa
  de falecido, vínculo e catálogo passaram com:
  go test -p 1 ./internal/modules/party ./internal/modules/catalog -run '^TestDevelopment(DeceasedHTTP|FamilyLinkHTTP|CatalogHTTP)Integration$' -count=1 -v.
  O container temporário foi removido. Na execução ampla inicial, rotinas
  antigas tentaram inserir o tenant pelo papel de aplicação (42501) e o
  cleanup de falecido recebeu erro de atualização concorrente (XX000). A execução serial
  dirigida resolveu a colisão; os testes legados precisam de fixture por DSN
  administrativo. Grants de aplicação foram preservados.
- O estado da prévia vive na aba e o CSP impede conexões pelo aplicativo.
  O teste em PostgreSQL avalia os handlers privados separadamente. Ficam
  pendentes gravação operacional pela web, OIDC real e sincronização ponta a
  ponta entre estação, relay e SaaS.
- Fatia #72 (PF autenticado, ADR 0041): `go test -p 1 ./internal/modules/party
  -run '^TestProductionPartiesIntegration' -count=1 -v` passou com 8 casos em
  PostgreSQL 16 temporário, migrations até 0035 e papel `amparius_app` sem
  superuser e sem bypass de RLS; o container foi removido. Os casos cobrem 401,
  403 por papel, empresa sem vínculo, falha fechada sem provedor, criação e
  busca persistidas, isolamento entre tenants, busca que não consome o limite
  com pessoa jurídica e ausência de PII em erros e logs. Os dados são sintéticos
  e o verificador OIDC é de teste. Não são evidência de produção, de OIDC/MFA,
  de UI real nem das suítes legadas. O [ADR 0042](../adr/0042-falecido-vinculo-familiar-consentimento-v1.md#verificação)
  registra a validação independente da #73 com política sintética e duplos
  de OIDC/MFA, incluindo RLS, rollback, concorrência, legado e limpeza.
  Provedor OIDC/MFA real, política DPO, UI operacional e emissão fiscal
  estão fora da evidência. A [#82](https://github.com/alltomatos/amparius/issues/82)
  acompanha o rascunho 0036 no banco dev compartilhado.
