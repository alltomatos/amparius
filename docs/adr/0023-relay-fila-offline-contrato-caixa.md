# ADR 0023 — Fila offline de contrato e caixa e log monotônico por dispositivo

Status: aceito em 25/11/2026 — RELAY-01B

## Contexto

O RELAY-01A entregou store temporário, credencial em cache e snapshot de enforcement, mas
ainda não existia o registro das operações que a loja produz sem internet. Sem esse registro,
uma queda de rede durante contrato ou caixa perderia o efeito remoto ou permitiria duplicação
na reconexão. O PRD exige pelo menos 24 h de operação offline, fila idempotente e status
visível; o ADR 0002 exige sequência por nó, não relógio, e proíbe last-write-wins silencioso
em dinheiro ou fiscal.

Esta fatia cobre apenas contrato e caixa. Emissão fiscal em contingência, transmissão,
sincronização e reconciliação continuam nas fatias RELAY-01C e RELAY-02.

## Decisão

1. Toda operação offline vira um registro **append-only** em
   `relay::queue::QueuedOperation`, persistido no mesmo store local versionado do RELAY-01A.
   O store passa ao schema `2`; arquivos `1` são migrados na abertura sem perder registros,
   credenciais ou snapshot.
2. Cada dispositivo tem um `node_id` e uma sequência `seq: u64` monotônica por
   `(tenant_id, node_id)`. O último valor é persistido em `DeviceSequencer`; na abertura o
   relay recalcula o máximo a partir do log e nunca reutiliza ou reduz a sequência.
3. A idempotência local é a chave `(tenant_id, node_id, idempotency_key)`. Replay com o mesmo
   conteúdo devolve o registro existente sem criar outro; a mesma chave com conteúdo
   diferente falha com `IdempotencyConflict` e **não sobrescreve** o registro anterior.
4. O conteúdo relevante é resumido em `content_hash` SHA-256 sobre
   `(kind, entity_id, payload)`. `operation_id` é um hash opaco de
   `(tenant_id, node_id, seq, content_hash)`. Na abertura, hash, identificador,
   unicidade de sequência, unicidade de idempotência e unicidade de dispositivo
   são verificados; adulteração falha fechado.
5. O payload da fila aceita somente referências e valores operacionais mínimos. Valores
   monetários são inteiros em centavos (`total_cents`, `opening_amount_cents`,
   `amount_cents`, `counted_amount_cents`), com sinal e mínimo validados por tipo. Chaves de
   dado pessoal conhecidas (CPF, CNPJ, RG, nome, e-mail, telefone, endereço, nascimento) são
   recusadas; o dado de negócio completo fica na projeção local, não no log de sincronização.
6. O relay expõe a fila por HTTP v1 autenticado pela credencial em cache: `POST
   /relay/v1/operations`, `GET /relay/v1/operations` e `GET /relay/v1/status`. O tenant vem
   da credencial autenticada, nunca do corpo. O token bruto não é persistido nem logado.
7. O status é um agregado por tenant e, opcionalmente, por nó: pendentes, sincronizados,
   falhos, conflitos, total, sequência final, instante e idade do pendente mais antigo e
   último erro. `mode` é `offline_queued` quando há pendente e `online` quando não há. A UI
   usa contagem e idade para mostrar a fila; contingência fiscal entra quando a fatia fiscal
   existir.
8. Transições de status são monotônicas no sentido de não regressão: `synced` é idempotente,
   mas não volta para `failed`/`conflict`; `conflict` não vira `synced` sem resolução
   explícita de fatia posterior. Métodos de transição existem na biblioteca para o
   sincronizador futuro; nenhum endpoint público os expõe nesta fatia.

## Invariantes de segurança e dados

- Operação só é aceita com `AuthenticatedSubject` válido e dentro da janela da credencial
  em cache; expiração ou token incorreto nega a escrita.
- Toda leitura e escrita é filtrada por `tenant_id`; não existe consulta sem tenant e outro
  tenant não enxerga sequência, contagem nem conteúdo.
- O arquivo local continua ignorado pelo Git e não contém token bruto, chave privada,
  certificado ou segredo.
- A escrita é feita em arquivo temporário com backup recuperável. Se a persistência falhar,
  o registro recém-inserido e o avanço da sequência são revertidos em memória: a operação
  só é confirmada depois de durável em disco.
- Não há last-write-wins: a fila é append-only e o conflito de idempotência é erro.

## Consequências

- A loja consegue registrar contrato e caixa durante a queda de rede e reiniciar o relay sem
  perder a ordem local; o status mostra o backlog e sua idade.
- O store JSON é suficiente para a janela mínima de 24 h desta fatia, mas não substitui
  WAL/atomicidade do PostgreSQL para dinheiro ou fiscal em produção contínua. A migração para
  PostgreSQL local continua prevista no ADR 0002 e nas fatias posteriores.
- O sincronizador precisa transportar `(node_id, seq, idempotency_key, content_hash)` e
  tratar `conflict` como tarefa de revisão; isso é contrato da RELAY-02, não desta ADR.
- A emissão fiscal em contingência não é calculada nem estimada aqui. Quando entrar, usará
  as tabelas datadas e o motor de tributos oficiais, com estado próprio.
- A autenticação por cabeçalho pressupõe loopback ou TLS local terminado no relay; expor o
  relay sem TLS violaria o transporte de credencial.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo clippy --manifest-path relay/Cargo.toml --all-targets -- -D warnings
cargo test --manifest-path relay/Cargo.toml
```

Contrato executável: `docs/contracts/relay-operations-v1.md`.
