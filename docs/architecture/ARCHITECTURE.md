# Arquitetura — Amparius

Versão 0.1 · 21/11/2026

## 1. Forma da solução

```
┌────────────── cliente (Rust/PWA) ──────────────┐
│ desktop de operação · app de campo · portal    │
└───────────────┬────────────────────────────────┘
                │ HTTPS (API + SSE)
        ┌───────▼────────┐        ┌──────────────────┐
        │ Relay local    │◀──────▶│ SaaS Amparius    │
        │ (Rust)         │  sync  │ (Go)             │
        │ Postgres local │        │ Postgres · Redis │
        │ fila de saída  │        │ RabbitMQ         │
        └───────┬────────┘        └───────┬──────────┘
                │                         │
        ┌───────▼────────┐        ┌───────▼──────────┐
        │ SEFAZ/prefeitura│        │ Provedores IA    │
        │ (via SaaS)      │        │ MCP servers      │
        └────────────────┘        └──────────────────┘
```

**Decisão central:** **monólito modular** no SaaS (um binário `api`, um `worker`) + **relay de borda** separado. Extrair serviço só quando houver evidência de gargalo ou isolamento regulatório.

## 2. Contexto (C4 nível 1)

| Ator/Sistema | Relação |
|---|---|
| Operador da funerária/cemitério/floricultura | usa cliente e relay no dia a dia |
| Gestor/financeiro | painel SaaS, relatórios, fechamento |
| Contador | exportações fiscais/contábeis |
| SEFAZ / prefeituras | emissão e consulta de documentos fiscais |
| Bancos | conciliação (OFX/CNAB) e cobrança (boleto/PIX) |
| Provedores de IA | inferência para copilotos |
| Serviços MCP | ferramentas externas autorizadas por tenant |
| Receita Federal | tabelas e regras (RTC, NCM, CFOP) |

## 3. Contêineres

| Contêiner | Tecnologia | Responsabilidade |
|---|---|---|
| `amparius-api` | Go (HTTP) | API multi-tenant, autenticação, casos de uso síncronos |
| `amparius-worker` | Go | eventos, jobs, emissão fiscal assíncrona, relatórios |
| `amparius-relay` | Rust | operação local, fila idempotente, sync, cache de auth |
| `amparius-web` | (a definir) | painel SaaS e portal do cliente |
| `amparius-client` | Rust | desktop de operação (fase posterior) |
| PostgreSQL | 16+ | dados transacionais, RLS, outbox |
| Redis | 7+ | cache, locks, rate limit, idempotência curta |
| RabbitMQ | 3.13+ | eventos de domínio e jobs |
| Object storage | S3-compatible | XML, PDF, anexos, backups lógicos |

> **Control plane (ADR 0005/0007 §9):** além dos contêineres acima, existe o **control plane** separado, fonte da verdade para billing + entitlement: guarda cota por módulo e por acessos/usuários, licenças atribuídas por CNPJ (com vigência), e a auditoria de ativação/desativação. Não guarda dado de negócio. Célula e relay mantêm apenas o snapshot assinado de enforcement (versão + TTL).

## 4. Módulos (bounded contexts)

| Módulo | Responsabilidade | Eventos públicos (exemplos) |
|---|---|---|
| `identity` | tenant, filial, usuário, papel, sessão, MFA | `tenant.created`, `user.invited` |
| `party` | PF/PJ, endereço, contato, falecido, familiar | `party.created`, `party.merged` |
| `catalog` | produto, serviço, plano, preço, NCM/LC116 | `catalog.item.updated` |
| `finance` | plano de contas, título, parcela, baixa, caixa, ledger | `finance.title.created`, `finance.payment.settled` |
| `fiscal` | configuração, motor de tributos, documento, transmissão, DANFE | `fiscal.document.authorized`, `fiscal.document.rejected` |
| `funeral` | contrato, OS, agenda de velório/sepultamento, locais e recursos, traslado (sem cremação) | `funeral.contract.created`, `funeral.schedule.created`, `funeral.schedule.cancelled` |
| `cemetery` | cemitério, quadra, jazigo, concessão, sepultamento | `cemetery.burial.registered` |
| `cremation` | crematório, cremação, urna, autorização (módulo próprio — ADR 0007, independente de funeral/cemetery) | `cremation.service.registered` |
| `floriculture` | arranjo, pedido, entrega, rota, lote | `floriculture.order.dispatched` |
| `inventory` | depósito, item, lote, movimento | `inventory.moved` |
| `documents` | arquivo, template, assinatura, retenção | `document.stored` |
| `ai` | agente, execução, ferramenta, conhecimento | `ai.run.finished` |
| `sync` | nó, cursor, operação, conflito | `sync.batch.applied` |
| `reporting` | views materializadas e consultas analíticas | — |
| `entitlement` | concern transversal de enforcement (licença por empresa/CNPJ, cota por módulo, bloqueio em 3 camadas UI/backend/relay) | `entitlement.revoked`, `entitlement.assigned` |

Regras: schema por módulo, sem `JOIN` cruzado em código de módulo, integração por serviço público ou evento versionado.

### FUN-01B — fronteira do parcelamento

O módulo `funeral` mantém somente o snapshot em
`funeral.contract_installment`. A criação publica
`finance.title.requested` v1 e `fiscal.document.requested` v1 pela outbox;
nenhum módulo faz JOIN ou escrita direta no schema vizinho. As chaves de
idempotência são derivadas do `contract_id`, evitando duas solicitações para a
mesma cobrança ou documento. Os consumidores financeiros e fiscais ainda são
uma fatia posterior; o worker atual não deve marcar esses eventos como efeito
de negócio concluído.

### FUN-01C — fronteira da ordem de serviço

`funeral.service_order` é um agregado operacional do contrato. A criação exige
contrato ativo e grava OS, histórico inicial e evento em uma única transação.
Update e conclusão usam `service_order_mutation` para replay idempotente; o
histórico permanece append-only. A consulta filtra tenant pela RLS e empresa
pelo contexto confiável.

Os eventos `funeral.service_order.created`,
`funeral.service_order.updated`, `funeral.service_order.status_changed` e
`funeral.service_order.completed` v1 não
carregam descrição, nomes ou qualquer dado de party. Não existe JOIN com
`party`, `finance` ou `fiscal` no módulo funeral. Consumidores de negócio e
sincronização do relay continuam sendo fatias posteriores.

### FUN-02A — agenda e recursos funerários

`funeral.schedule` reserva uma janela semiaberta `[starts_at, ends_at)` em UTC,
com o nome IANA `time_zone` explícito. Cada agenda referencia o contrato e o
local por UUID opaco, podendo também referenciar uma OS e um recurso do local.
As constraints de exclusão do PostgreSQL impedem sobreposição de local ou
recurso, inclusive em duas inserções concorrentes; `[14:00,15:00)` é adjacente
e não conflita com `[12:00,14:00)`.

`schedule_status_history` e `schedule_mutation` são append-only, e a criação,
alteração/cancelamento e evento v1 são transacionais. O módulo não faz JOIN
com `party`, `cemetery`, `finance` ou `fiscal`; a integração futura ocorre por
referência opaca e eventos versionados.

## 5. Dados

- **PostgreSQL** com schema por módulo e `tenant_id` em toda tabela de negócio; **RLS** obrigatória.
- Chaves `uuidv7`; dinheiro `NUMERIC(18,2)` (ou `int64` centavos na aplicação, com conversão única).
- `audit_log` append-only; `outbox` transacional para eventos.
- Partições por mês/tenant em tabelas de alto volume (`ledger_entry`, `fiscal_document`, `sync_operation`, `ai_run`).
- Redis nunca é fonte de verdade: cache com TTL, locks com expiração e chave por tenant.
- Object storage para XML/PDF com hash SHA-256 e retenção legal (5 anos para fiscal).

## 6. Relay e offline

### 6.1 Responsabilidades

1. Autenticar operador com credencial em cache (token de dispositivo).
2. Executar operações locais permitidas offline (venda, contrato, caixa, OS, sepultamento).
3. Enfileirar efeitos que exigem servidor (emissão fiscal, eventos, integrações).
4. Sincronizar em lotes ordenados, com idempotência e reconciliação.

### 6.2 Modelo de sincronização

- Cada dispositivo tem `node_id` e um log `sync_operation(seq, entity, op, payload, hash, created_at)`.
- Servidor aplica operações por `idempotency_key`; resposta traz novo `cursor`.
- Entidades mestras (catálogo, preço, usuário): **servidor autoritativo**; relay aplica snapshot com versão.
- Entidades operacionais (contrato, título, OS): **origem autoritativa** com carimbo `origin_node`; conflito em campo crítico (valor, documento fiscal) vira **tarefa de revisão**, nunca sobrescrita silenciosa.
- Relógio: servidor mantém offset por nó; sequência manda, não timestamp.

### 6.3 Limites explícitos do offline

| Funciona offline | Não funciona offline |
|---|---|
| abrir contrato/OS, receber pagamento, caixa, agendar velório/sepultamento | consultar SEFAZ/prefeitura em tempo real |
| emitir documento em contingência (quando a regra permitir) | contratar novo tenant, alterar plano do SaaS |
| consultar catálogo e preço em cache | relatórios consolidados multiempresa |
| ler histórico local | ações que exigem aprovação central |

A UI mostra o estado: **online**, **offline com fila** e **contingência fiscal**, com contagem e idade da fila.

### 6.4 Modos de instalação e acesso

Um **único instalador** (Windows e Linux primeiro; macOS depois) com dois papéis escolhidos na configuração:

| Papel | O que faz |
|---|---|
| **Estação** | cliente desktop do operador; fala com o SaaS ou com o servidor da loja |
| **Servidor da loja (relay)** | banco local, servidor web na LAN, fila de sincronização e base temporária |

No papel de servidor da loja:

- PostgreSQL local + servidor web HTTPS para os terminais do balcão;
- **base temporária**: cadastros, catálogo e preços, tabelas fiscais vigentes, contratos/OS abertos, caixa, agenda e últimos 90 dias de histórico (janela configurável, busca sob demanda);
- credencial de operador emitida pelo SaaS e validada localmente (cache com validade);
- fila idempotente e sincronização ao reconectar.

Acesso em três caminhos com a **mesma interface**: portal web do SaaS (online), web local servida pelo servidor da loja (offline ou LAN) e desktop.

Segurança: matrícula do dispositivo no control plane, chave por instalação, TLS local emitido no provisionamento, escuta apenas na LAN por padrão; túnel de saída para acesso remoto é opcional e desligado.

Detalhes e consequências: ADR 0006.

## 7. Fiscal

### 7.1 Camadas

```
documento (intenção) → cálculo (motor) → XML (gerador) → validação (XSD)
   → transmissão (adaptador) → retorno (persistido) → PDF (DANFE/DANFSe)
```

- **Motor de tributos**: regras por data de competência, tabelas versionadas (NCM, CST, cClassTrib, alíquotas, LC 116, ISS municipal).
- **RTC**: IBS/CBS/IS com tabelas oficiais; o motor aceita regime geral e Simples Nacional. Ver `docs/research/receita-federal.md`.
- **Adaptadores**: NFS-e por município/provedor; NF-e/NFC-e por SEFAZ (autorizador por UF); contingência conforme regra.
- **Certificado**: A1 no servidor (por empresa/CNPJ, não por tenant) e no relay quando a emissão é local; alerta de expiração em 30 dias. Configuração/certificado fiscal são por CNPJ (ADR 0007 §10).
- **Idempotência**: `idempotency_key` = tenant + tipo + série + número + hash do conteúdo.
- **Auditoria**: XML enviado, XML retornado, protocolo, hash, usuário, horário, modo (normal/contingência).

### 7.2 Fluxo online

1. Caso de uso grava `fiscal_document` como `draft` na mesma transação do fato gerador.
2. Worker calcula tributos e gera XML; valida XSD.
3. Transmite; persiste retorno cru; publica `fiscal.document.authorized|rejected`.
4. Gera PDF; anexa ao contrato/pedido; reflete no financeiro.

### 7.3 Fluxo com relay

1. Relay grava documento local como `pending_contingency` e devolve comprovante local.
2. Sync envia ao SaaS; worker transmite quando houver conectividade e dentro do prazo.
3. Sucesso → `authorized`; falha → tarefa com motivo e prazo; UI mostra pendência.
4. Documento emitido localmente em contingência é reconciliado e, se necessário, substituído.

## 8. IA embutida

- **Gateway MCP** interno: cada módulo expõe ferramentas tipadas; o gateway aplica tenant, papel, allowlist e limites.
- **Agentes por papel**: atendimento, cobrança, fiscal, cemitério, análise. Padrão somente leitura.
- **Execução com permissão**: escrita exige confirmação explícita e gera `ai_tool_call` auditado.
- **Conhecimento**: documentos do tenant indexados com permissão por módulo; prompts versionados.
- **PII**: mascaramento antes do provedor; retenção de prompt/resposta configurável por tenant.
- **Custo**: orçamento por tenant/mês, com corte e alerta.

## 9. Segurança e LGPD

- OIDC (Keycloak ou provedor gerenciado) + MFA; sessão curta no relay com refresh.
- RBAC (papel) + ABAC (filial, módulo, registro).
- Segredos em Vault/KMS; certificados criptografados com chave por tenant.
- Criptografia em repouso e TLS 1.3 em trânsito; backups criptografados.
- LGPD: inventário de dados, base legal, retenção, anonimização e trilha de consentimento para dados de familiares/falecidos.
- Logs sem PII; acesso a dado sensível gera evento de auditoria próprio.

## 10. Observabilidade e operação

- OpenTelemetry (trace, métrica, log) com `tenant_id` e `module` como atributos.
- SLOs: disponibilidade 99,9%, p95 de leitura 200 ms, fila de sync < 5 min, emissão pendente < 1% ao dia.
- Alertas: fila de relay crescendo, certificado expirando, rejeição fiscal acima do baseline, erro de sync por nó.
- Runbooks: queda de SEFAZ, certificado inválido, nó offline prolongado, divergência de saldo.

## 11. Implantação

| Ambiente | Topologia |
|---|---|
| Dev | docker compose (Postgres, Redis, RabbitMQ, MinIO) + api/worker locais |
| Staging | 1 região, mesma topologia de produção, dados sintéticos |
| Produção | **células** em VPS com Docker (ver `SCALING.md` e ADR 0005): uma VPS por conjunto de tenants, control plane separado, Postgres com PITR e réplica por célula |
| Relay | instalado no cliente (Windows/Linux), Postgres local, serviço com auto-start |

Capacidade inicial: 1 célula M (~120 tenants), 20 mil documentos fiscais/mês, 500 usuários simultâneos. Crescimento por adição de células; revisar após 3 meses de operação.

## 12. Falhas esperadas e resposta

| Falha | Comportamento |
|---|---|
| SEFAZ indisponível | contingência; fila com prazo; alerta |
| Internet do cliente cai | relay opera; UI mostra fila; sync ao voltar |
| Postgres do SaaS cai | api responde 503; relay continua local |
| RabbitMQ atrasa | outbox garante reentrega; worker idempotente |
| Certificado expirado | bloqueia emissão com mensagem clara; não corrompe documento |
| Divergência de sync | tarefa de revisão; nada é sobrescrito em dinheiro/fiscal |
