# DER — Amparius

Versão 0.2 · modelo lógico inicial + entidade legal, estabelecimento e entitlement (ADR 0007). Diagramas em Mermaid; nomes em `snake_case`; toda tabela de negócio carrega `tenant_id`.

> **Decisão de nomes (ADR 0007 §7):** `COMPANY` é a **entidade legal operadora** (CNPJ raiz — a funerária, o cemitério, a floricultura). O subtipo de cadastro de PJ cliente/fornecedor, antes chamado `COMPANY`, passa a se chamar **`PARTY_COMPANY`**. Não existem duas entidades com o mesmo nome. `ESTABLISHMENT` substitui `BRANCH`.

## 1. Núcleo: identidade, cadastro e catálogo

```mermaid
erDiagram
  TENANT ||--o{ COMPANY : operates
  COMPANY ||--o{ ESTABLISHMENT : has
  TENANT ||--o{ USER : employs
  USER }o--o{ ROLE : receives
  ROLE }o--o{ PERMISSION : grants
  TENANT ||--o{ PARTY : owns
  PARTY ||--o| PERSON : is
  PARTY ||--o| PARTY_COMPANY : is
  PARTY ||--o{ ADDRESS : has
  PARTY ||--o{ CONTACT : has
  PERSON ||--o| DECEASED : refers_to
  DECEASED ||--o{ FAMILY_LINK : linked
  FAMILY_LINK }o--|| PERSON : relative
  TENANT ||--o{ CATALOG_ITEM : sells
  CATALOG_ITEM ||--o{ PRICE : priced_in
  PRICE_LIST ||--o{ PRICE : contains
  CATALOG_ITEM }o--|| TAX_CLASSIFICATION : classified_as

  TENANT { uuid id PK
           text name
           text document
           text regime
           text status }
  COMPANY { uuid id PK
            uuid tenant_id FK
            text document
            text name
            text kind
            text status
            timestamptz created_at
            timestamptz updated_at
            timestamptz deleted_at }
  ESTABLISHMENT { uuid id PK
                  uuid tenant_id FK
                  uuid company_id FK
                  text document
                  text name
                  text state_registration
                  text municipal_registration
                  text street
                  text number
                  text city
                  text state
                  text zip
                  bool is_headquarters
                  text status
                  timestamptz created_at
                  timestamptz updated_at
                  timestamptz deleted_at }
  USER { uuid id PK
         uuid tenant_id FK
         text email
         text name
         text status
         bool mfa_enabled }
  ROLE { uuid id PK
         text code
         text name }
  PERMISSION { uuid id PK
               text resource
               text action }
  PARTY { uuid id PK
          uuid tenant_id FK
          text kind
          text name
          text document
          text status }
  PERSON { uuid party_id PK
           date birth_date
           text gender }
  PARTY_COMPANY { uuid party_id PK
                  text trade_name
                  text state_registration
                  text municipal_registration }
  ADDRESS { uuid id PK
            uuid party_id FK
            text street
            text number
            text city
            text state
            text zip
            bool primary }
  CONTACT { uuid id PK
            uuid party_id FK
            text kind
            text value
            bool primary }
  DECEASED { uuid id PK
             uuid person_id FK
             timestamptz death_at
             text death_certificate
             text burial_site }
  FAMILY_LINK { uuid id PK
                uuid deceased_id FK
                uuid person_id FK
                text relation }
  CATALOG_ITEM { uuid id PK
                 uuid tenant_id FK
                 text kind
                 text name
                 text unit
                 bool active }
  PRICE_LIST { uuid id PK
               uuid tenant_id FK
               text name
               date valid_from }
  PRICE { uuid id PK
          uuid item_id FK
          uuid list_id FK
          numeric amount
          date valid_from
          date valid_to }
  TAX_CLASSIFICATION { uuid id PK
                       text ncm
                       text cest
                       text cst
                       text class_trib
                       text service_code
                       date valid_from }
```

## 2. Espinha dorsal: financeiro e fiscal

```mermaid
erDiagram
  TENANT ||--o{ ACCOUNT : owns
  ACCOUNT ||--o{ ACCOUNT : parent_of
  TENANT ||--o{ COST_CENTER : owns
  TENANT ||--o{ BANK_ACCOUNT : holds
  TENANT ||--o{ TITLE : has
  TITLE ||--o{ INSTALLMENT : split_into
  INSTALLMENT ||--o{ PAYMENT : settled_by
  BANK_ACCOUNT ||--o{ PAYMENT : receives
  TENANT ||--o{ CASH_SESSION : opens
  CASH_SESSION ||--o{ CASH_MOVEMENT : records
  PAYMENT ||--o{ LEDGER_ENTRY : posts
  TITLE ||--o{ LEDGER_ENTRY : posts
  BANK_ACCOUNT ||--o{ RECONCILIATION : reconciles
  COMPANY ||--|| FISCAL_CONFIG : configures
  FISCAL_CONFIG ||--o{ CERTIFICATE : uses
  TENANT ||--o{ TAX_RULE : applies
  TENANT ||--o{ FISCAL_DOCUMENT : issues
  COMPANY ||--o{ FISCAL_DOCUMENT : bills_as
  ESTABLISHMENT ||--o{ FISCAL_DOCUMENT : issued_at
  FISCAL_DOCUMENT ||--o{ FISCAL_ITEM : contains
  FISCAL_ITEM ||--o{ TAX_CALC : calculated
  FISCAL_DOCUMENT ||--o{ EMISSION_ATTEMPT : attempted

  TITLE { uuid id PK
          uuid tenant_id FK
          text direction
          uuid party_id FK
          numeric amount
          date due_date
          text status
          text origin }
  INSTALLMENT { uuid id PK
                uuid title_id FK
                int number
                numeric amount
                date due_date
                text status }
  PAYMENT { uuid id PK
            uuid installment_id FK
            uuid bank_account_id FK
            numeric amount
            timestamptz paid_at
            text method }
  CASH_SESSION { uuid id PK
                 uuid tenant_id FK
                 uuid user_id FK
                 timestamptz opened_at
                 timestamptz closed_at
                 numeric opening_amount }
  CASH_MOVEMENT { uuid id PK
                  uuid session_id FK
                  text kind
                  numeric amount
                  text reason }
  LEDGER_ENTRY { uuid id PK
                 uuid tenant_id FK
                 uuid account_id FK
                 date competence
                 numeric debit
                 numeric credit
                 text source }
  FISCAL_CONFIG { uuid company_id PK
                  uuid tenant_id FK
                  text regime
                  text municipality_code
                  text state
                  text series_nfse
                  text series_nfe }
  CERTIFICATE { uuid id PK
                uuid tenant_id FK
                uuid company_id FK
                text subject
                timestamptz valid_until
                text storage_ref }
  TAX_RULE { uuid id PK
             text code
             text scope
             numeric rate
             date valid_from
             date valid_to }
  FISCAL_DOCUMENT { uuid id PK
                    uuid tenant_id FK
                    uuid company_id FK
                    uuid establishment_id FK
                    text kind
                    text series
                    int number
                    text status
                    text contingency_mode
                    uuid party_id FK
                    numeric total
                    timestamptz issued_at }
  FISCAL_ITEM { uuid id PK
                uuid document_id FK
                uuid item_id FK
                numeric quantity
                numeric unit_price
                numeric total }
  TAX_CALC { uuid id PK
             uuid item_id FK
             text tax_type
             numeric base
             numeric rate
             numeric amount
             text rule_code }
  EMISSION_ATTEMPT { uuid id PK
                     uuid document_id FK
                     int attempt
                     text mode
                     text result
                     text protocol
                     text xml_hash }
```

## 3. Setores: funerário, cemitério e floricultura

> `cremation` é módulo próprio (ADR 0007), **não** sub-funcionalidade de `funeral`; suas entidades (crematório, cremação, urna) são modeladas no módulo `cremation` e não neste bloco inicial.

```mermaid
erDiagram
  TENANT ||--o{ CONTRACT : sells
  CONTRACT ||--o{ SERVICE_ORDER : triggers
  CONTRACT }o--|| FUNERAL_PLAN : uses
  CONTRACT ||--o{ FISCAL_DOCUMENT_REF : bills
  SERVICE_ORDER ||--o| WAKE : schedules
  TENANT ||--o{ CEMETERY : operates
  CEMETERY ||--o{ BLOCK : divided_into
  BLOCK ||--o{ PLOT : contains
  PLOT ||--o{ CONCESSION : granted
  PLOT ||--o{ BURIAL : receives
  BURIAL ||--o| EXHUMATION : may_have
  TENANT ||--o{ ARRANGEMENT : composes
  ARRANGEMENT ||--o{ ARRANGEMENT_ITEM : uses
  TENANT ||--o{ ORDER : sells
  ORDER ||--o{ ORDER_ITEM : contains
  ORDER ||--o| DELIVERY : delivered_by
  TENANT ||--o{ LOT : tracks
  LOT ||--o{ INVENTORY_MOVEMENT : moves

  CONTRACT { uuid id PK
             uuid tenant_id FK
             uuid party_id FK
             uuid deceased_id FK
             numeric total
             text status
             date signed_at }
  FUNERAL_PLAN { uuid id PK
                 uuid tenant_id FK
                 text name
                 numeric monthly
                 text coverage }
  SERVICE_ORDER { uuid id PK
                  uuid contract_id FK
                  text kind
                  text status
                  timestamptz scheduled_for }
  WAKE { uuid id PK
         uuid service_order_id FK
         text venue
         timestamptz starts_at
         timestamptz ends_at }
  CEMETERY { uuid id PK
             uuid tenant_id FK
             text name
             text city }
  BLOCK { uuid id PK
          uuid cemetery_id FK
          text code }
  PLOT { uuid id PK
         uuid block_id FK
         text code
         text status
         numeric area }
  CONCESSION { uuid id PK
               uuid plot_id FK
               uuid party_id FK
               date starts_on
               date ends_on
               text status }
  BURIAL { uuid id PK
           uuid plot_id FK
           uuid deceased_id FK
           timestamptz buried_at
           uuid service_order_id FK }
  EXHUMATION { uuid id PK
               uuid burial_id FK
               timestamptz done_at
               text destination }
  ARRANGEMENT { uuid id PK
                uuid tenant_id FK
                text name
                numeric price }
  ORDER { uuid id PK
          uuid tenant_id FK
          uuid party_id FK
          text status
          numeric total }
  DELIVERY { uuid id PK
             uuid order_id FK
             timestamptz window_start
             timestamptz window_end
             text address
             text status }
  LOT { uuid id PK
        uuid tenant_id FK
        uuid item_id FK
        text code
        date expires_on }
  INVENTORY_MOVEMENT { uuid id PK
                       uuid lot_id FK
                       numeric quantity
                       text kind
                       timestamptz moved_at }
  FISCAL_DOCUMENT_REF { uuid id PK
                        uuid contract_id FK
                        uuid fiscal_document_id FK }
```

## 4. Plataforma: sync, IA e auditoria

```mermaid
erDiagram
  TENANT ||--o{ RELAY_NODE : registers
  RELAY_NODE ||--o{ SYNC_OPERATION : produces
  SYNC_OPERATION ||--o| SYNC_CONFLICT : may_raise
  TENANT ||--o{ OUTBOX_EVENT : publishes
  TENANT ||--o{ AI_AGENT : configures
  AI_AGENT ||--o{ AI_RUN : executes
  AI_RUN ||--o{ AI_TOOL_CALL : calls
  AI_AGENT }o--o{ KNOWLEDGE_DOC : uses
  TENANT ||--o{ MCP_SERVER : allows
  TENANT ||--o{ AUDIT_LOG : records

  RELAY_NODE { uuid id PK
               uuid tenant_id FK
               text name
               text fingerprint
               timestamptz last_seen }
  SYNC_OPERATION { uuid id PK
                   uuid node_id FK
                   bigint seq
                   text entity
                   text operation
                   jsonb payload
                   text idempotency_key
                   timestamptz created_at }
  SYNC_CONFLICT { uuid id PK
                  uuid operation_id FK
                  text field
                  jsonb local_value
                  jsonb remote_value
                  text status }
  OUTBOX_EVENT { uuid id PK
                 uuid tenant_id FK
                 text topic
                 jsonb payload
                 timestamptz published_at }
  AI_AGENT { uuid id PK
             uuid tenant_id FK
             text name
             text role
             text model
             jsonb tool_allowlist }
  AI_RUN { uuid id PK
           uuid agent_id FK
           uuid user_id FK
           text status
           int tokens_in
           int tokens_out
           numeric cost }
  AI_TOOL_CALL { uuid id PK
                 uuid run_id FK
                 text tool
                 jsonb input
                 jsonb output
                 text status }
  KNOWLEDGE_DOC { uuid id PK
                  uuid tenant_id FK
                  text title
                  text source_ref
                  text status }
  MCP_SERVER { uuid id PK
               uuid tenant_id FK
               text name
               text url
               text status }
  AUDIT_LOG { uuid id PK
              uuid tenant_id FK
              uuid actor_id
              text action
              text resource
              jsonb detail
              timestamptz at }
```

## 5. Entitlement e módulos — vivem no **control plane**

> **Banco:** o schema `entitlement` e o catálogo `module` pertencem ao **Postgres do control plane** (ADR 0005), junto de billing, diretório de tenants e provisionamento. **Nenhuma dessas tabelas existe na célula.** A célula e o relay guardam apenas um **snapshot assinado** de enforcement (ver nota ao final da seção) — não é tabela de negócio.

```mermaid
erDiagram
  TENANT ||--o{ LICENSE_POOL : has_quota
  TENANT ||--|| USER_QUOTA : has_seats
  TENANT ||--o{ LICENSE_ASSIGNMENT : holds
  TENANT ||--o{ ENTITLEMENT_EVENT : audits
  MODULE ||--o{ LICENSE_POOL : pooled_as
  MODULE ||--o{ LICENSE_ASSIGNMENT : licenses
  MODULE ||--o{ ENTITLEMENT_EVENT : concerns
  COMPANY ||--o{ LICENSE_ASSIGNMENT : entitled_by
  COMPANY ||--o{ ENTITLEMENT_EVENT : concerns
  MODULE }o--|| MODULE_KIND : classified_as
  LICENSE_ASSIGNMENT }o--|| LICENSE_STATUS : has
  ENTITLEMENT_EVENT }o--|| ENTITLEMENT_ACTION : performs
  ENTITLEMENT_EVENT }o--|| ENTITLEMENT_ORIGIN : originates_from

  MODULE { text code PK
           text name
           text kind FK
           int sort_order
           bool active }
  MODULE_KIND { text code PK
                text name }
  LICENSE_POOL { uuid id PK
                 uuid tenant_id FK
                 text module_code FK
                 int quota
                 timestamptz updated_at }
  LICENSE_ASSIGNMENT { uuid id PK
                       uuid tenant_id FK
                       text module_code FK
                       uuid company_id FK
                       date valid_from
                       date valid_to
                       text status FK
                       timestamptz created_at
                       timestamptz updated_at }
  LICENSE_STATUS { text code PK
                   text name }
  USER_QUOTA { uuid tenant_id PK
               int seats
               int used
               timestamptz updated_at }
  ENTITLEMENT_EVENT { uuid id PK
                      uuid tenant_id FK
                      text module_code FK
                      uuid company_id FK
                      uuid actor_id
                      text action FK
                      text origin FK
                      timestamptz at }
  ENTITLEMENT_ACTION { text code PK
                       text name }
  ENTITLEMENT_ORIGIN { text code PK
                       text name }
```

**Catálogo `MODULE` — 14 módulos (7 núcleo, 7 opcionais):**

| `kind` | `code` | Observação |
| --- | --- | --- |
| `core` | `identity` | fora da cota |
| `core` | `party` | fora da cota |
| `core` | `catalog` | fora da cota |
| `core` | `finance` | fora da cota |
| `core` | `fiscal` | fora da cota; leitura congelada garantida |
| `core` | `sync` | fora da cota |
| `core` | `reporting` | fora da cota; leitura congelada garantida |
| `optional` | `funeral` | na cota |
| `optional` | `cemetery` | na cota |
| `optional` | `cremation` | na cota; **distinto de `funeral`** |
| `optional` | `floriculture` | na cota |
| `optional` | `inventory` | na cota |
| `optional` | `documents` | na cota |
| `optional` | `ai` | na cota |

**Regras do schema de entitlement:**

- A coluna de módulo é `module_code`, FK para `module.code`; o código é estável e nunca é reciclado. Todos os `text ... FK` de enum acima apontam para a tabela de domínio correspondente (regra 7), nunca para `enum` do Postgres.
- `LICENSE_POOL` é a **cota por módulo por tenant** concedida pelo admin SaaS. A ocupação não é coluna digitada: é contada a partir de `LICENSE_ASSIGNMENT` com licença vigente.
- `LICENSE_ASSIGNMENT`: **1 licença = 1 módulo em 1 empresa (CNPJ)**. `company_id` aponta para `COMPANY`; filial **não** recebe licença — herda da empresa. `valid_from`/`valid_to` são `date` (vigência comercial) e são o gancho de cobrança.
- `USER_QUOTA`: cota de acessos/usuários do tenant (`seats`), com `used` alimentado pelo reporte de ocupação da célula, não por FK entre bancos.
- `ENTITLEMENT_EVENT` é **append-only**: toda ativação/desativação/revogação, por self-service ou admin SaaS, com `actor_id` (usuário do diretório global do control plane, sem FK para a célula) e `at`. Nunca é atualizado nem apagado.
- **Nenhum** desses registros é aceito do corpo da requisição do cliente; `tenant_id` e `company_id` vêm da sessão autenticada.
- **Emissão fiscal** exige cumulativamente: `LICENSE_ASSIGNMENT` vigente para `module_code = 'fiscal'` no `company_id` do fato gerador + `FISCAL_CONFIG`/`CERTIFICATE` daquele `company_id`. Regras de alíquota/CST/NCM continuam vindo exclusivamente das tabelas oficiais datadas (ADR 0004).
- **Snapshot de enforcement (célula e relay):** pacote assinado contendo `version` monotônica e `valid_until` (TTL), derivado destas tabelas. Não vira tabela na célula; o relay aplica **fail-closed** ao expirar e trata `entitlement.revoked` como operação de sync prioritária.

## 6. Regras de modelagem

1. **Chaves**: `uuidv7` em todas as tabelas; `bigint` sequencial só em log de sync por nó.
2. **Tenant**: `tenant_id NOT NULL` + política RLS `USING (tenant_id = current_setting('app.tenant_id')::uuid)`.
3. **Dinheiro**: `NUMERIC(18,2)`; nunca `float`. Totais recalculados no servidor, não confiados do cliente.
4. **Tempo**: `timestamptz` em UTC; competência fiscal é `date`.
5. **Exclusão**: `deleted_at` (soft delete) em cadastros; ledger, fiscal e auditoria nunca são apagados.
6. **Versionamento**: `version int` em entidades sincronizáveis; `updated_at` sempre.
7. **Enums**: códigos estáveis em tabela de domínio, não `enum` do banco, para permitir evolução.
8. **Partições**: `ledger_entry`, `fiscal_document`, `fiscal_item`, `sync_operation`, `ai_run`, `audit_log` e `entitlement_event` por mês (e tenant quando necessário).
9. **Índices**: sempre `(tenant_id, ...)`; busca textual só em `reporting`/índice dedicado.
10. **Fiscal**: `fiscal_document` guarda o XML em object storage com `xml_hash`; o banco guarda metadados e o retorno cru por tentativa.
11. **Empresa e estabelecimento**: `COMPANY` é a entidade legal (CNPJ raiz) e recebe entitlement, `fiscal_config` e `certificate`; `ESTABLISHMENT` (substitui `BRANCH`) é o estabelecimento/filial e herda o entitlement da empresa. Onde o ADR 0003 diz `branch_id`, lê-se `establishment_id`; `company_id` passa a ser escopo secundário adicional nas políticas de acesso.
12. **Fronteira control plane**: `module` e `entitlement` vivem no Postgres do control plane; célula e relay só mantêm o snapshot assinado de enforcement (versão + TTL). Nenhuma cópia de tabela de licença na célula.
13. **Migração fiscal forward-only**: `fiscal_config` sai de `tenant_id PK` para `company_id PK` e `certificate` troca `tenant_id` por `company_id` em **migração nova**; a migração existente não é editada. A carga inicial exige `COMPANY` criada por tenant antes do `NOT NULL`.
14. **Fiscal não se estima**: nenhuma coluna deste modelo (nem `tax_rule`, nem `tax_calculation`) carrega alíquota, CST, cClassTrib, NCM ou CFOP fixos; valores vêm de tabela oficial datada, consultada pela data de competência.
