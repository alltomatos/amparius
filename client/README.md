# Amparius Client

Cliente desktop shell do Amparius no papel de estação.

## Requisitos

- Rust MSRV: 1.85 (edition 2024)

## Executar

```powershell
cargo run
```

O binário abre a janela nativa do painel do dia (CL-01). A estação exige
identidade (`tenant` e `node`); sem ela a janela abre em modo de configuração e
nenhuma fila é criada.

## Executar o painel do dia (CL-01)

```powershell
$env:AMPARIUS_TENANT = "loja-1"
$env:AMPARIUS_NODE = "estacao-1"
cargo run --manifest-path client/Cargo.toml
```

Equivalente por argumento: `--tenant loja-1 --node estacao-1`. Opcionais:

| Variável / argumento | Uso |
| --- | --- |
| `AMPARIUS_QUEUE_PATH` / `--queue` | arquivo da fila local (padrão em `%LOCALAPPDATA%\Amparius\client\`) |
| `AMPARIUS_RELAY_URL` / `--relay` | origem do relay, ex. `http://127.0.0.1:8787` |
| `AMPARIUS_SUBJECT` / `--subject` | identidade do operador no relay |
| `AMPARIUS_TOKEN` | credencial de sessão; **apenas ambiente**, nunca exibida nem persistida |

Sem relay/credencial a tela mostra "Sincronização indisponível" com o motivo e o
botão desabilitado; nada é simulado.

A tela mostra online/offline/contingência, contadores da fila
(pendentes/sincronizadas/falhas/conflitos/novas tentativas), operações
priorizadas (conflito e falha primeiro), tarefas de revisão, códigos técnicos
sanitizados e o resultado da sincronização. O botão **Sincronização e
revisão** abre a tela CL-05 com backlog, duplicadas, falhas, conflitos e
tarefas abertas. **Tentar novamente** reapresenta somente `pending`; **Abrir
revisão** é somente leitura. Conflito nunca é resolvido pela tela e o valor
local é preservado. Contrato: `docs/contracts/client-day-panel-v1.md` e
`docs/contracts/client-sync-review-v1.md`;
decisão de stack: `docs/adr/0036-cliente-ui-nativa-egui-painel-dia.md`.
Decisão específica da tela: `docs/adr/0037-cliente-sync-review.md`.
## Atendimento funeral (CL-02)

A tela nativa "Atendimento funeral" usa referências opacas e permite três
operações idempotentes na fila local:

- criar jornada atômica (`funeral_journey_create`), com OS e agenda;
- avançar a OS para `in_progress`;
- pausar a OS com `funeral_service_order_pause`;
- concluir a OS com `funeral_service_order_complete`.

O contrato `active` precisa ser confirmado na tela e é revalidado pelo servidor;
contrato inativo, entitlement ausente e identidade do operador bloqueiam a ação.
Em `offline` ou `contingency`, a operação é persistida como `queued` com `seq` e
`Idempotency-Key`; em `online`, o relay precisa estar configurado. Serviço
indisponível nunca é apresentado como sucesso. A tela não coleta nome,
documento, contato ou descrição livre, e não cria/edita partes — isso fica para
um endpoint autorizado futuro.

Configuração explícita do módulo em ambiente de desenvolvimento:

```powershell
$env:AMPARIUS_SUBJECT = "operator-1"
$env:AMPARIUS_FUNERAL_ENTITLEMENT = "active"
```

Sem `AMPARIUS_FUNERAL_ENTITLEMENT=active`, a tela fica bloqueada por fail-closed.
As operações funerárias são aceitas pelo relay e permanecem pendentes até o
consumidor/serviço de negócio correspondente estar disponível.


## Ordens de serviço (CL-03)

A mesma tela lista as ordens conhecidas pela fila local e mostra referência opaca,
estado operacional, versão esperada, `seq` e estado de sincronização (`queued`,
`done`, `error` ou `conflict`). Selecionar uma ordem preenche a versão-base para
avançar, pausar ou concluir. Enquanto houver mutação pendente, falha terminal ou
conflito, a ordem fica bloqueada para nova mutação; conflito exige revisão e não
usa last-write-wins.

As chaves são próprias de cada mutação (`cl03-<ação>-<ordem>-<expected_version>`)
e o payload leva `expected_version`. A lista é uma projeção reconstruída de
`ClientQueue`, portanto sobrevive ao reinício sem um segundo store local. A
projeção cobre as mutações conhecidas pela estação; a autoridade final de estado e
versão continua sendo o serviço funeral no servidor.

## Modelo CL-01/CL-03

`src/lib.rs` contém o modelo de estado usado pela futura UI:

- `ConnectivityState` separa `online`, `offline` e `contingency`; a operação permitida continua enfileirável nos três estados.
- `ClientQueue` atribui `seq` monotônica por `(tenant_id, node_id)` e mantém a mesma operação em replay.
- A mesma `idempotency_key` com conteúdo diferente falha com `IdempotencyConflict`; dinheiro e fiscal nunca aplicam last-write-wins.
- `ReconciliationOutcome::Conflict` e `FiscalRejected` criam tarefas explícitas de revisão sem carregar payload, resposta remota ou PII.
- `ClientStatus` e `ClientMetrics` expõem apenas contagens, estado e códigos técnicos seguros para observabilidade.
- `ClientQueue::open(path, tenant_id, node_id)` persiste a fila em JSON versionado com SHA-256, escrita temporária/backup e rejeição fail-closed de corrupção ou versão desconhecida.

O arquivo local cobre a fila da estação e o reinício do cliente, mas não substitui o armazenamento durável do relay.

## Transporte HTTP

`src/transport.rs` envia a fila persistida ao relay local no contrato RELAY-OPERATIONS v1:

```rust
use amparius_client::transport::{Credentials, HttpTransport, TransportConfig, sync_pending};

let mut config = TransportConfig::new("http://127.0.0.1:8787")?;
config.timeout = std::time::Duration::from_secs(5);
config.max_attempts = 3;
let transport = HttpTransport::new(config)?;
let credentials = Credentials::new("operator-1", token)?;

let report = sync_pending(&mut queue, &transport, &credentials, amparius_client::unix_now()).await?;
```

- Preserva `(tenant_id, node_id)`, `Idempotency-Key`, `seq` monotônica, `operation_id` e `content_hash`; revalida payload e recusa PII.
- Timeout configurável; retry limitado apenas para timeout/indisponibilidade/5xx, com backoff exponencial e jitter determinístico.
- 409 vira conflito explícito; demais 4xx são rejeição sem retry (fiscal vira `FiscalRejected`, os outros viram `Conflict`).
- Replay do relay (`replayed: true`) é `Duplicate`, sucesso idempotente sem criar operação.
- `master_upsert` só é marcado sincronizado com `authoritative_version`; sem ela, vira revisão.
- Em queda, a execução para após a primeira falha transitória e a operação continua `pending`.
- O transporte é HTTP (loopback ou atrás de TLS local): o token nunca é logado nem persistido, e redirecionamentos são desabilitados.

## UI do painel do dia

- `src/ui/day_panel.rs` é o modelo puro (estados, contadores, priorização,
  códigos sanitizados, horário em America/Sao_Paulo); `src/ui/render.rs` desenha
  com `eframe`/`egui` e `src/ui/app.rs` liga janela, fila persistida e
  sincronização em thread.
- A faixa de marca e o ícone da janela usam
  [o símbolo oficial do Amparius](../assets/brand/amparius-icon.svg). O PNG de
  64 px é a versão rasterizada desse SVG, carregada uma vez no início da janela.
- Tokens em `src/ui/theme.rs` (`#00274C`, `#0073CF`, estados com texto +
  símbolo). Não há cópia de componentes/tema de outro ERP.
- Atualizações de conexão são declaradas pelo operador e persistidas na fila;
  contingência não autoriza emissão fiscal.
- Limite de acessibilidade registrado: `egui`/AccessKit não oferece `aria-live`
  nem tabela nativa; teclado, foco visível, alvos de 44 px, texto + símbolo e
  título de janela com o estado são cobertos.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

Medições desta entrega (build `dev`, Windows, 26/11/2026): 51 testes verdes,
`fmt` e `clippy -D warnings` limpos, primeiro quadro registrado em
`boot_ms=129` e janela aberta com o título refletindo o estado
(`Amparius - Online - 0 pendente(s)`). A fila real em disco foi criada na
primeira execução.

