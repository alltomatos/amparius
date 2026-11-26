# Contrato CLIENT-DAY-PANEL v1

Escopo: primeira superfície gráfica do cliente Rust (`client/`), tela **CL-01 —
Painel do dia**. Consome `ClientQueue` persistida (contrato
`client-offline-state-v1`) e o transporte HTTP do relay. Não cria contrato HTTP
novo nem regra fiscal.

Implementação: `client/src/ui/day_panel.rs` (modelo), `client/src/ui/render.rs`
(desenho), `client/src/ui/app.rs` (janela, sincronização e relógio). Decisão de
stack em [ADR 0036](../adr/0036-cliente-ui-nativa-egui-painel-dia.md).

## Fonte de dados

| Origem | Uso na tela |
| --- | --- |
| `ClientQueue::connectivity()` | faixa de conexão (`online`, `offline`, `contingency`) |
| `ClientQueue::status()` | contadores pendentes, sincronizadas, falhas, conflitos, novas tentativas e revisões abertas |
| `ClientQueue::operations()` | lista de operações (`seq`, tipo, status, tentativas, `created_at`, `last_error`) |
| `ClientQueue::review_tasks()` | bloco de revisão (`kind`, `code`, operação) |
| `transport::sync_pending` + `SyncReport` | bloco de sincronização e última sincronização |

Nunca são exibidos: payload, XML fiscal, resposta do relay, token,
`tenant_id`, `node_id` ou dado pessoal. `entity_id` só aparece como referência
opaca já validada em `lib.rs` (`[A-Za-z0-9_-.:]`, até 128 caracteres).

## Estados obrigatórios

### Fase da tela

| Fase | Quando | Apresentação |
| --- | --- | --- |
| `loading` | antes do primeiro estado pronto (modelo `DayPanelView::loading`) | título, faixa de conexão e resumo "Carregando o estado da estação." |
| `ready` | fila aberta | contadores, operações, revisão, sincronização e rodapé |
| `error` | abertura/leitura da fila falhou | bloco de erro com código, causa e ação; sincronização bloqueada |

A janela sem `tenant`/`node` abre a tela de configuração obrigatória e **não
cria fila** (falha fechada): tenant nunca é inventado.

### Conexão

| Estado | Texto | Símbolo | Regra |
| --- | --- | --- | --- |
| `online` | Online | `+` | fila local disponível |
| `offline` | Offline | `-` | fila continua local; nada é perdido |
| `contingency` | Contingência | `~` | operação local ativa; **contingência não autoriza emissão fiscal** |

Cor nunca é o único sinal: sempre há símbolo (ASCII) + texto. O operador declara
o estado; a escolha é persistida por `ClientQueue::set_connectivity`, e os três
estados continuam aceitando enfileiramento.

### Operação

| Status da fila | Rótulo | Severidade | Prioridade |
| --- | --- | --- | --- |
| `conflict` | Conflito | perigo | 0 |
| `failed` | Falhou | perigo | 1 |
| `pending` com `attempts > 0` | Nova tentativa | atenção | 2 |
| `pending` | Pendente | informação | 3 |
| `synced` | Sincronizada | sucesso | 4 |

Ordenação final: prioridade crescente e, dentro dela, `seq` crescente.
`needs_review` é verdadeiro quando existe tarefa de revisão aberta para a
operação; a linha mostra "Requer revisão; o valor local foi preservado.".
Nenhum valor local é sobrescrito pela tela.

### Sincronização

| Situação | Rótulo | Ação |
| --- | --- | --- |
| relay/credencial ausente | Sincronização indisponível + motivo | botão desabilitado; nada é simulado |
| sem pendências | Nada pendente | botão desabilitado |
| pendências | Pronto para sincronizar | "Sincronizar agora" habilitado |
| execução | Sincronizando | botão desabilitado; indicador de progresso |
| fim | Sincronização concluída + contagens | novo clique só se restar pendência |
| erro | Sincronização falhou + código | botão habilitado para nova execução |

`SyncReport` é traduzido em contagens (`synced`, `duplicates`, `conflicts`,
`rejected`, `retried`, `remaining_pending`) e motivo de parada
(`transport_timeout`, `transport_unavailable`, `unauthorized`). Nenhum texto
contém PII.

## Erros de fila

`PanelError::from_queue_error` traduz a falha local em código estável, sem
caminho nem conteúdo:

`empty_tenant`, `empty_node`, `unsupported_store_version`, `corrupt_store`,
`tenant_mismatch`, `node_mismatch`, `invalid_store`, `io_error`, `queue_error`.

O bloco mostra mensagem, código e próxima ação; ao aparecer, o foco vai para o
botão "Tentar novamente".

## Ações

| Ação | Efeito | Persistência |
| --- | --- | --- |
| escolher conexão | `set_connectivity` | imediata no arquivo da fila |
| "Sincronizar agora" | `sync_pending` em thread com runtime próprio; fila em `Arc<Mutex<_>>` | cada resultado passa por `reconcile`, que persiste antes de confirmar |
| "Atualizar"/"Recarregar estado" | reabre a fila do disco | leitura |
| "Tentar novamente" (erro) | reabre a fila | leitura |

Bloqueios: "Atualizar" é recusado enquanto uma sincronização roda. A tela não
resolve conflito, não reaplica local e não cria atendimento/ordem de serviço —
isso pertence a CL-02/CL-03/CL-05.

## Acessibilidade e responsividade

- Layout de coluna única com cartões; sem scroll horizontal em 360 px; largura
  máxima de conteúdo 1200 px.
- Alvos com altura mínima de 44 px; espaçamento de 8 px.
- Navegação por teclado e foco visível (anel de 2 px) vêm do `egui`; rótulos são
  texto, não ícone isolado.
- O título da janela resume o estado (`Amparius — Offline — 3 pendente(s)`),
  dando ao leitor de tela uma mudança observável.
- **Limite registrado:** `egui`/AccessKit não oferece região "live" equivalente a
  `aria-live` nem semântica de tabela. A paridade total de anúncios pertence à
  superfície web do servidor da loja (ADR 0006), que reutilizará este contrato.
  Teste manual com leitor de tela segue pendente.

## Observabilidade

- `screen = "CL-01"` em log estruturado.
- `client first frame` com `boot_ms` (tempo até o primeiro quadro).
- Fim de sincronização registra apenas contagens e motivos técnicos.
- Nenhum log contém payload, token, XML ou PII.

## Testes

- `client/src/ui/day_panel.rs`: 9 testes de estado (vazio, offline,
  contingência, conflito/falha priorizados, ausência de identidade/payload,
  indisponibilidade de sync, erro fatal, horário local, sanitização).
- `client/src/ui/render.rs`: 3 testes de renderização headless com `egui_kittest`
  (árvore de UI real: vazio, conflito/revisão e erro fatal com ação).
- `client/src/ui/app.rs`: 2 testes de mapeamento de `SyncReport` e configuração.
- Suíte total do cliente: 39 testes.

## Fora de escopo / limites

- Sem emissão fiscal, sem atendimento, sem ordem de serviço e sem resolução de
  conflito nesta fatia.
- Sincronização é HTTP sem TLS embutido (ADR 0035), exige relay em loopback ou
  atrás de TLS local.
- A fila local é JSON versionado com hash, sem criptografia em repouso nem
  concorrência entre processos (ADR 0034).
- A superfície LAN/web do servidor da loja não é entregue aqui.
