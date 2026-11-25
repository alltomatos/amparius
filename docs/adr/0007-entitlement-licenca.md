# ADR 0007 — Entitlement de módulos por licença

Status: proposto · 21/11/2026 · referencia os ADRs 0002, 0003, 0005 e 0006

## Contexto

No Amparius, os módulos são habilitados conforme o plano/licença contratado. Empresas têm conjuntos diferentes de módulos: o cliente A contrata funerária + floricultura; o B, funerária + cemitério; o C, cemitério + cremação. O sistema precisa representar isso sem que um módulo desligado quebre os demais, e sem deixar de cobrar/liberar corretamente — inclusive quando a operação roda offline no relay (ADR 0002/0006).

Há duas tensões de fundo:

1. **Cobrança ainda não existe** (vem depois, na gestão/vendas), mas o modelo de entitlement precisa deixar o gancho pronto para não retrabalhar.
2. **O DER atual não distingue "empresa (CNPJ)" de "filial"**: `BRANCH` não tem documento, e o ADR 0003 trata filial apenas como escopo secundário (`branch_id`). O entitlement precisa amarrar em um nível com CNPJ, porque é nesse nível que a configuração fiscal e o certificado vivem.

## Decisão

### 1. Módulos e cota

1. **Cremação é módulo próprio**, independente de funerário e de cemitério (não é sub-funcionalidade de `funeral`).
2. **Núcleo sempre ligado, fora da cota**: `identity`, `party`, `catalog`, `finance`, `fiscal`, `sync`, `reporting`. **Opcionais, dentro da cota**: `funeral`, `cemetery`, `cremation`, `floriculture`, `inventory`, `documents`, `ai`.
3. **Módulos são independentes**: ligar/desligar um não quebra o resto do sistema. Nenhum módulo opcional é pré-condição rígida de outro; dependências pontuais (ex.: cremação referenciando contratos) degradam para somente leitura quando o vizinho está desligado.

### 2. Comportamento de desativação

4. **Desativar bloqueia o uso** do módulo (novas operações); **reativar restaura tudo**; **dados nunca são apagados**. Desligar um módulo não remove, não anonimiza nem expurga os dados dele — apenas impede escrita/uso novo.

### 3. Bloqueio em três camadas

5. O bloqueio é aplicado em **três camadas**, todas derivando da mesma fonte de verdade:
   - **UI**: o módulo some do menu e das ações no cliente/portal;
   - **Backend**: a API nega endpoint, evento e chamada de IA do módulo sem licença ativa (falha antes de tocar o dado);
   - **Relay offline**: o relay respeita o entitlement em cache (ver §7).

### 4. Gestão mista, auditoria e fonte da verdade

6. Gestão **mista**: o cliente ativa/desativa módulos em self-service, mas **a licença ativa é a fonte da verdade** — sem licença válida, ativar não funciona; o **admin SaaS sobrepõe** qualquer estado. Toda mudança de estado gera **auditoria obrigatória** (quem ligou/desligou, quando, por qual origem: self-service ou admin SaaS).

### 5. Cobrança

7. **Cobrança fica para depois**, mas o modelo grava licença com **vigência datada** (`valid_from`/`valid_to`), deixando o gancho para gestão/vendas cobrar sem retrabalho.

### 6. Granularidade: grupo econômico, empresa, CNPJ

8. **Tenant = grupo econômico**; dentro dele, **várias empresas (CNPJ)**, matriz + filiais; a prática do setor é **"um CNPJ por segmento"** (a funerária é um CNPJ, o cemitério é outro).
9. **Entitlement é por empresa/CNPJ** dentro do tenant — não por tenant e não por filial.
10. O **admin SaaS libera, por tenant**: uma **cota de licenças POR módulo** (ex.: 2 licenças de `cemetery`) + uma **cota de acessos/usuários**. **1 licença = habilita aquele módulo em 1 CNPJ**; o dono do tenant distribui as licenças entre os CNPJs dele.

### 7. Tensão de modelo: empresa/CNPJ vs BRANCH (ponto central)

Hoje o DER tem `BRANCH` sem documento e o ADR 0003 trata filial só como escopo secundário. Isso conflita com "entitlement por empresa/CNPJ". A reconciliação é registrar **dois níveis distintos**:

- **(a) Empresa / entidade legal** — CNPJ raiz, nível em que o entitlement amarra, a configuração fiscal vive e o certificado é emitido. A funerária e o cemitério são empresas (entidades legais) distintas, mesmo dentro do mesmo grupo econômico.
- **(b) Estabelecimento / filial** — mesmo CNPJ raiz, com endereço/IE distintos (matriz e filiais da mesma empresa).

**Decisão: o entitlement amarra na empresa/entidade legal (CNPJ raiz).** A filial herda o entitlement da empresa a que pertence; filial não recebe licença própria.

**Mudança no DER** (desenho completo fica com o Engenheiro de Dados na etapa seguinte): introduzir a entidade **`COMPANY`** (empresa/entidade legal, com `document` = CNPJ raiz, `tenant_id`, `status`, `kind`/segmento) e a entidade **`ESTABLISHMENT`** (estabelecimento/filial, com `document` = CNPJ da filial, `state_registration`/IE, `municipal_registration`, endereço), em substituição/ajuste de `BRANCH`. Observação de colisão de nome: o DER já tem uma `COMPANY` como subtipo de `PARTY` (cadastro de PJ cliente/fornecedor); os dois conceitos são distintos — o Engenheiro de Dados deve renomear um deles (sugestão: manter `COMPANY` para a entidade legal operadora e renomear o subtipo de cadastro para `PARTY_COMPANY`, ou usar `LEGAL_ENTITY` para a operadora).

### 8. Leitura congelada de fiscal e reporting (suposição formalizada)

**Decisão:** `fiscal` e `reporting` leem o **histórico congelado mesmo com o módulo desligado** — senão o contador não fecha o mês. Desligar um módulo bloqueia **escrita/uso novo**, mas a leitura de dados já existentes para fins fiscais/contábeis continua. Isso não é exceção ao bloqueio; é o contrato explícito de que a desativação afeta a operação, nunca a integridade do fechamento.

### 9. Fronteira control plane / célula / relay

A licença mora em um lugar só; enforcement e cache são cópias derivadas:

- **Control plane — fonte da verdade** (ADR 0005 já coloca billing nele): guarda a cota por módulo, a cota de acessos, as licenças atribuídas por CNPJ (com vigência) e a auditoria de ativação/desativação. Não guarda dado de negócio.
- **Célula — enforcement**: recebe do control plane um **pacote de entitlement assinado e versionado** (mesmo mecanismo das tabelas fiscais em `SCALING.md` §7), mantém um snapshot em cache e **nega** endpoint/evento/IA quando o módulo não tem licença ativa no CNPJ da empresa. Valida o self-service do cliente contra a licença ativa antes de aceitar.
- **Relay — cache de enforcement**: mantém o entitlement em cache com **validade (TTL)**. O cancelamento de um módulo se propaga por **invalidação** — versão monotônica do pacote + evento de revogação (`entitlement.revoked`) tratado como operação de sync prioritária. Offline, o relay respeita o último snapshot; a segurança vem do **fail-closed por TTL**: expirada a validade sem renovação, o módulo bloqueia (em vez de continuar liberado por tempo indefinido).

### 10. Regra fiscal

**O entitlement não pode, em hipótese alguma, permitir emissão fiscal sem o módulo `fiscal` ativo e sem licença válida.** Emissão fiscal exige, cumulativamente: (i) módulo `fiscal` ativo; (ii) licença válida no CNPJ da empresa à qual o fato gerador pertence; (iii) configuração e certificado fiscal daquela empresa. Nenhuma alíquota/CST/NCM é inventada aqui — isso segue o ADR 0004 (tabelas oficiais datadas).

**Registro da mudança fiscal:** a configuração/certificado fiscal é **por CNPJ (empresa)**, não por tenant. Hoje `FISCAL_CONFIG` é `tenant_id PK` e `CERTIFICATE` aponta para `tenant_id`; ambos migram para o nível da empresa/entidade legal. A emissão usa config/certificado do **CNPJ da empresa à qual o módulo está ligado**.

## Consequências

- O enforcement do entitlement passa a ser um pré-requisito transversal de **todo** endpoint, evento e chamada de IA — um ponto único de negação, não um bloco espalhado.
- Duas cópias de enforcement (célula e relay) exigem contrato de cache explícito: versão, assinatura, TTL e comportamento de expiração testados.
- O cancelamento de um módulo não é instantâneo em um relay offline: propaga-se na reconexão, limitado pelo TTL. A UI deve dizer quando um módulo bloqueou por expiração de cache.
- O fechamento mensal do contador depende de a leitura congelada de `fiscal`/`reporting` estar garantida independentemente do estado de entitlement.
- A entidade `COMPANY`/`ESTABLISHMENT` altera o modelo de identidade: RLS e políticas que hoje consideram `tenant_id` + `branch_id` passam a considerar também o nível empresa.
- Configuração e certificado fiscal por CNPJ multiplicam o número de configs por tenant (uma por empresa), o que afeta provisionamento, renovação de certificado e alertas de expiração.

## Alternativas descartadas

- **Entitlement por tenant (em vez de por CNPJ)**: simples, mas obriga o grupo a pagar módulo para todos os CNPJs mesmo quando só um segmento usa, e não casa com a configuração fiscal por CNPJ.
- **Entitlement por filial**: granular demais e sem documento próprio hoje; gera licença para endereço, não para entidade legal.
- **Fonte da verdade na célula (em vez do control plane)**: duplica billing entre células e viola o ADR 0005, que concentra billing no control plane.
- **Relay liberado por tempo indeterminado quando offline**: quebra a regra de cancelamento; trocado por fail-closed com TTL.
- **Apagar dados ao desativar módulo**: inaceitável por LGPD/contabilidade e por fechamento fiscal; dados são preservados.

## Critérios de aceite

1. Os 14 módulos estão classificados como núcleo (7, fora da cota) ou opcional (7, na cota), e `cremation` é módulo distinto de `funeral`.
2. Com um módulo sem licença ativa no CNPJ, a UI esconde o menu, a API nega endpoint/evento/IA e o relay (com cache válido) bloqueia o uso — os três em teste automatizado.
3. Desligar um módulo não apaga nenhum dado; reativar restaura o uso; `fiscal` e `reporting` continuam lendo o histórico congelado.
4. Ativar módulo via self-service falha quando não há licença ativa; admin SaaS sobrepõe; toda mudança gera registro de auditoria com quem/quando/origem.
5. Cancelamento de módulo propaga-se para o relay por invalidação (versão/evento) e, offline, o cache expira com TTL e bloqueia (fail-closed) — sem emissão fiscal pós-revogação.
6. Emissão fiscal só ocorre com `fiscal` ativo + licença válida + config/certificado do CNPJ da empresa; teste de contrato cobre a negação.
7. O DER passa a distinguir empresa/entidade legal (com `document`) de estabelecimento/filial, e o entitlement referencia a empresa; `FISCAL_CONFIG`/`CERTIFICATE` apontam para a empresa.

## Mudanças decorrentes

### DER (entidades/colunas sugeridas — desenho final com o Engenheiro de Dados)

- **`COMPANY`** (entidade legal operadora; se houver colisão com o subtipo `PARTY.COMPANY`, renomear um dos dois): `id`, `tenant_id`, `document` (CNPJ raiz), `name`, `kind`/segmento, `status`, `created_at`, `updated_at`.
- **`ESTABLISHMENT`** (estabelecimento/filial; substitui/ajusta `BRANCH`): `id`, `company_id`, `document` (CNPJ da filial), `state_registration` (IE), `municipal_registration`, endereço, `status`, `created_at`, `updated_at`.
- **Entitlement (schema `entitlement`, fonte de verdade no control plane):**
  - `LICENSE_POOL`: `tenant_id`, `module`, `quota` (cota de licenças por módulo).
  - `LICENSE_ASSIGNMENT`: `id`, `tenant_id`, `module`, `company_id`, `valid_from`, `valid_to`, `status` — 1 licença = 1 módulo em 1 empresa.
  - `USER_QUOTA` (cota de acessos): `tenant_id`, `seats`, `used`.
  - `ENTITLEMENT_EVENT` (auditoria): `id`, `tenant_id`, `module`, `company_id`, `actor_id`, `action`, `origin` (self-service | admin), `at`.
- **Snapshot de enforcement** (célula/relay): pacote assinado + `version` monotônica + `valid_until` (TTL), não tabela de negócio.

### ARCHITECTURE.md

- §3 (control plane): explicitar **billing + entitlement** como responsabilidade do control plane (fonte da verdade).
- §4 (módulos): adicionar `cremation` como módulo próprio e `entitlement` como concern transversal de enforcement.
- §7 (fiscal): configuração/certificado por **empresa/CNPJ**, não por tenant.
- `SCALING.md` §7: distribuição do pacote de entitlement assinado/versionado às células (par ao pacote de tabelas fiscais).

### PRD (RFs novos)

- RF: ativar/desativar módulo em self-service, com bloqueio nas três camadas (UI/backend/relay).
- RF: cota de licenças por módulo e cota de acessos/usuários por tenant; distribuição de licenças por CNPJ pelo dono do tenant.
- RF: auditoria de ativação/desativação (quem, quando, origem).
- RF: leitura congelada de `fiscal`/`reporting` com módulo desligado (fechamento do contador).
- RF: superposição do admin SaaS sobre o estado do tenant.

### Relay (cache de entitlement)

- Cache de entitlement com TTL e versão; invalidação por versão monotônica + evento `entitlement.revoked` tratado como operação de sync prioritária; fail-closed na expiração do cache; sinalização na UI quando um módulo bloqueia por expiração.

## Implementação (ENT-01C, 24/11/2026)

A issue #21 entrega o ciclo de vida da licença no control plane, apenas no que o
schema de `migrations/control/0001_entitlement.sql` já suporta.

1. **Auditoria append-only na mesma transação.** `internal/modules/entitlement/service.go`
   grava `entitlement.entitlement_event` com `tenant_id`, `module_code`,
   `company_id`, `actor_id`, `action`, `origin` e `at` dentro da transação que
   muda `license_assignment`. O `actor_id` vem do principal autenticado (sem FK
   entre bancos) e `origin` é `self_service` ou `admin`; autor ausente e origem
   desconhecida falham com erro tipado. Os triggers de `0001` bloqueiam
   `UPDATE`/`DELETE` no evento inclusive para o owner.
2. **Reativação sem perda de dados.** Desativar e revogar mudam apenas `status`
   e `updated_at`: a linha, `id`, `valid_from` e `valid_to` são preservados.
   Reativar reaproveita a mesma linha e mantém a vigência quando o chamador não
   informa datas novas; nenhuma operação apaga dado de negócio.
3. **Cota por módulo verificada na ativação.** A ocupação de `LICENSE_POOL` é
   contada a partir de `LICENSE_ASSIGNMENT` com `status = 'active'` e período
   sobreposto, nunca de coluna digitada. Sem pool provisionado, a ativação
   falha fechada com `ErrQuotaNotConfigured`; exceder a cota falha com
   `ErrQuotaExceeded`. Módulo `core` é imutável e fica fora da cota
   (`ErrCoreModuleImmutable`).
4. **Uma licença ativa por empresa e módulo.** A migration control forward-only
   `0002_entitlement_activation.sql` cria índice único parcial em
   `(tenant_id, module_code, company_id) WHERE status = 'active'`. Duplicidade
   ativa preexistente faz a migration falhar de propósito e exige
   reconciliação; histórico inativo/revogado continua livre.
5. **Cota de usuários por reporte.** `USER_QUOTA.seats` é provisionado pelo
   admin e `used` chega do reporte de ocupação da célula; o serviço recusa
   `used > seats` (`ErrQuotaExceeded`) e reporte sem cota provisionada
   (`ErrQuotaNotConfigured`). Reduzir a cota de um módulo abaixo das licenças
   ativas também é recusado (`ErrQuotaExceeded`), para não deixar o tenant
   acima da cota sem uma desativação explícita. A alteração de cota/assentos não gera evento nesta
   fatia porque o catálogo `entitlement_action` cobre apenas
   ativação/desativação/revogação.

**Permanece pendente:** enforcement HTTP/endpoint e cabeçalho de empresa
confiável (issue #20); reporte real de assentos pela célula; auditoria de
alteração de cota (exige nova ação no catálogo); snapshot assinado, invalidação
e TTL no relay. Nada de fiscal foi tocado.

