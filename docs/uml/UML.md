# UML — Amparius

Versão 0.1 · diagramas de componentes, sequência, estados e classes.

## 1. Componentes

```mermaid
flowchart LR
  subgraph Cliente
    UI[Desktop / PWA]
  end
  subgraph Borda
    RELAY[amparius-relay<br/>Rust]
    LDB[(PostgreSQL local)]
    LQ[[Fila de saída]]
  end
  subgraph SaaS
    API[amparius-api<br/>Go]
    WORKER[amparius-worker<br/>Go]
    PG[(PostgreSQL<br/>RLS)]
    REDIS[(Redis)]
    MQ[(RabbitMQ)]
    OBJ[(Object storage)]
  end
  subgraph Externos
    SEFAZ[SEFAZ / Prefeitura]
    BANK[Bancos / PIX]
    AI[Provedores de IA]
    MCP[Servidores MCP]
  end
  UI --> RELAY
  UI --> API
  RELAY --> LDB
  RELAY --> LQ
  RELAY <-->|sync| API
  API --> PG
  API --> REDIS
  API --> MQ
  WORKER --> MQ
  WORKER --> PG
  WORKER --> OBJ
  WORKER --> SEFAZ
  WORKER --> BANK
  API --> AI
  API --> MCP
```

## 2. Sequência — emissão fiscal online

```mermaid
sequenceDiagram
  autonumber
  participant U as Atendente
  participant API as amparius-api
  participant PG as PostgreSQL
  participant MQ as RabbitMQ
  participant W as worker
  participant S as SEFAZ/Prefeitura

  U->>API: fechar contrato + emitir NFS-e
  API->>PG: grava contrato, título e fiscal_document(draft)
  API->>MQ: publica fiscal.document.requested (outbox)
  API-->>U: contrato fechado (documento pendente)

  MQ->>W: consome pedido
  W->>PG: carrega dados, tabelas de tributos vigentes
  W->>W: calcula tributos e gera XML
  W->>W: valida XSD
  W->>S: transmite
  S-->>W: protocolo/autorização ou rejeição
  W->>PG: persiste tentativa, XML e retorno
  W->>PG: fiscal_document = authorized | rejected
  W->>MQ: publica fiscal.document.authorized
  W->>W: gera DANFSe (PDF) e anexa
  API-->>U: documento autorizado (via SSE/polling)
```

## 3. Sequência — operação offline e sincronização

```mermaid
sequenceDiagram
  autonumber
  participant U as Operador
  participant R as Relay
  participant L as Postgres local
  participant API as SaaS
  participant W as worker

  U->>R: abre contrato e recebe pagamento
  R->>L: grava contrato/título (origem local)
  R->>L: enfileira operação de sync
  R-->>U: recibo local + documento em contingência

  Note over R,API: internet volta
  R->>API: POST /sync/batch (node_id, seq, operações, hash)
  API->>API: valida idempotência e versão
  alt conflito crítico
    API-->>R: 409 com campos divergentes
    R-->>U: tarefa de revisão (nada sobrescrito)
  else aplicado
    API->>API: aplica, publica eventos
    API-->>R: cursor novo + confirmações
    R->>L: marca operações como aplicadas
  end
  API->>W: pedido de emissão fiscal pendente
  W-->>API: resultado da transmissão
```

## 4. Sequência — agente de IA com ferramenta protegida

```mermaid
sequenceDiagram
  autonumber
  participant U as Usuário
  participant API as api (ai gateway)
  participant AG as Agente
  participant M as MCP/Tool
  participant PG as PostgreSQL

  U->>API: "cobre os títulos vencidos do mês"
  API->>PG: carrega escopo do tenant + papel
  API->>AG: prompt + tools permitidas + contexto
  AG->>API: tool_call: finance.list_overdue(range)
  API->>API: valida permissão (leitura) e PII
  API->>M: executa ferramenta
  M-->>API: resultado
  API->>PG: registra ai_tool_call (auditoria)
  AG-->>API: proposta: "enviar 12 cobranças"
  API-->>U: pedido de confirmação (escrita)
  U->>API: confirma
  API->>M: executa escrita com idempotência
  API->>PG: registra resultado e custo
```

## 5. Estados

### 5.1 Documento fiscal

```mermaid
stateDiagram-v2
  [*] --> draft
  draft --> calculated: tributos calculados
  calculated --> validated: XSD ok
  calculated --> failed: erro de regra
  validated --> queued: enviado à fila
  queued --> transmitting: worker assume
  transmitting --> authorized: protocolo
  transmitting --> rejected: rejeição
  transmitting --> contingency: sem conexão/SEFAZ
  contingency --> authorized: transmitido no prazo
  contingency --> expired: prazo vencido
  authorized --> cancelled: cancelamento no prazo
  authorized --> replaced: substituição
  rejected --> calculated: corrigir e reenviar
  failed --> [*]
  cancelled --> [*]
  replaced --> [*]
```

### 5.2 Contrato funerário

```mermaid
stateDiagram-v2
  [*] --> draft
  draft --> awaiting_signature: gerado
  awaiting_signature --> active: assinado
  awaiting_signature --> cancelled: desistência
  active --> in_service: OS aberta
  in_service --> completed: serviço prestado
  active --> defaulted: inadimplência
  defaulted --> active: regularizado
  completed --> [*]
  cancelled --> [*]
```

### 5.3 Relay

```mermaid
stateDiagram-v2
  [*] --> starting
  starting --> online: conectado
  starting --> offline: sem conexão
  online --> syncing: lote em envio
  syncing --> online: ack
  syncing --> conflict: 409
  conflict --> online: revisão concluída
  online --> offline: perda de rede
  offline --> syncing: rede voltou
  offline --> degraded: fila acima do limite
  degraded --> syncing: rede voltou
  online --> [*]: desligado
```

## 6. Classes — núcleo financeiro

```mermaid
classDiagram
  class Money {
    +int64 cents
    +add(Money) Money
    +subtract(Money) Money
    +multiply(Rate) Money
  }
  class TaxRule {
    +string code
    +string scope
    +decimal rate
    +Date validFrom
    +Date validTo
    +appliesOn(Date) bool
  }
  class TaxEngine {
    +calculate(Item, Date, TaxContext) TaxResult
  }
  class Title {
    +UUID id
    +Direction direction
    +Money amount
    +Date dueDate
    +TitleStatus status
    +settle(Money) Payment
  }
  class Installment {
    +int number
    +Money amount
    +Date dueDate
    +settle(Money) Payment
  }
  class Payment {
    +Money amount
    +Date paidAt
    +PaymentMethod method
  }
  class LedgerEntry {
    +Account account
    +Money debit
    +Money credit
    +Date competence
  }
  class FiscalDocument {
    +DocumentKind kind
    +int number
    +FiscalStatus status
    +Money total
    +authorize(Protocol)
    +reject(Reason)
    +cancel(Reason, Date)
  }
  Title "1" o-- "*" Installment
  Installment "1" o-- "*" Payment
  Payment "1" --> "*" LedgerEntry
  TaxEngine "1" --> "*" TaxRule
  FiscalDocument "1" o-- "*" TaxResult
```
