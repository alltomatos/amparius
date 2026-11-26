# ADR 0036 — UI nativa do cliente (egui/eframe) e modelo de apresentação do painel do dia

- **Status:** aceito em 25/11/2026 — CL-01; estendido em 26/11/2026 — CL-02/CL-03/CL-05
- **Escopo:** primeira superfície gráfica do cliente de estação Rust (`client/`)
- **Relaciona-se com:** ADR 0001 (stack), ADR 0002 (operação offline), ADR 0006
  (instalador e papéis), ADR 0023/0034/0035 (fila e transporte), ADR 0026
  (arquitetura das superfícies) e `docs/design/frontend-spec.md`

## Contexto

O cliente Rust já tem fila offline persistida (`ClientQueue`) e transporte HTTP
classificado (`transport::sync_pending`), mas não tem superfície gráfica: o
binário só registra log e aguarda `Ctrl+C`. A fatia CL-01 (painel do dia) precisa
ser executável, mostrar o estado real da fila e ser verificável nos estados
online, offline e contingência.

Não havia escolha registrada de toolkit para o cliente. ADR 0001 exige binário
Rust único e previsível, sem runtime pesado na máquina do cliente; ADR 0026 diz
que o cliente operacional terá camada de UI própria no workspace Rust, sem copiar
componentes, CSS ou estrutura de outro ERP. `frontend-spec.md` exige estado
explícito (idle/loading/empty/error/success), cor nunca como único sinal,
responsividade a partir de 360 px e acessibilidade WCAG 2.2 AA.

ADR 0006 prevê que o servidor da loja sirva a mesma interface aos terminais da
LAN. Esta ADR não resolve essa superfície web; ela define apenas a estação
desktop desta fatia e preserva o contrato de estado para que a superfície LAN
reuse o mesmo modelo, tokens e códigos — sem criar um terceiro produto de dados.

## Decisão

1. **Toolkit: `eframe`/`egui` 0.37, backend `glow`, features `accesskit` e
   `default_fonts`.** Janela nativa, binário único, sem WebView, sem Node, sem
   IPC JavaScript. `egui` é imediato, o que mantém a UI fina e o estado no Rust.
2. **A UI não contém regra de negócio.** `client/src/ui/day_panel.rs` produz um
   `DayPanelView` puro e determinístico a partir de `ClientQueue`, fase da tela,
   estado de sincronização e relógio injetado. É esse modelo que os testes
   cobrem; `render.rs` apenas desenha. Um apresentador futuro (LAN/web) consome o
   mesmo contrato, não o código de widget.
3. **Estados são explícitos e testáveis.** `PanelPhase` cobre
   `Loading/Ready/Error`; conectividade cobre `online/offline/contingency`;
   operações cobrem `pending/synced/failed/conflict`; sincronização cobre
   `idle/running/completed/unavailable/failed`. Não existe tela em branco.
4. **Nada de last-write-wins na UI.** Linha em `conflict` ou `failed` mostra
   código técnico filtrado e o aviso de que o valor local foi preservado e exige
   revisão. A UI não resolve conflito; CL-05 registra a tarefa e abre seu
   detalhe somente leitura, sem aplicar last-write-wins.
5. **Sem PII e sem payload.** O modelo expõe apenas `seq`, tipo de operação,
   referência opaca (`entity_id` já validado em `lib.rs`), estado, tentativas,
   horário local, idade e código técnico sanitizado. Payload, XML fiscal,
   resposta do relay, token e tenant/dispositivo não são exibidos nem logados.
   `DayPanelView::visible_text()` existe para teste e serve de resumo textual.
6. **Sincronização real e explícita.** O botão “Sincronizar agora” chama
   `sync_pending` sobre a fila persistida em uma thread, com `Arc<Mutex<_>>`; a
   UI nunca bloqueia no socket. Sem relay/credencial configurados, o estado é
   `unavailable` com motivo e o botão fica desabilitado — nunca se simula
   sucesso (critério CL-A03).
7. **Conectividade é declarada e persistida** por um seletor na tela
   (`ClientQueue::set_connectivity`), porque contingência é decisão do operador,
   não inferência de rede. Os três estados continuam permitindo enfileirar.
8. **Tokens próprios** em `client/src/ui/theme.rs` (`#00274C`, `#0073CF`, estados
   com texto + símbolo), sem copiar tema externo. Tema claro/escuro segue o
   sistema, sempre com contraste de texto ≥ 4.5:1 nas combinações usadas.

## Acessibilidade e divergência registrada

- Alvos de interação com altura mínima de 44 px; foco visível com anel de 2 px;
  navegação por Tab/Enter nativa do `egui`; todo status tem texto e símbolo, nunca
  só cor; o título da janela resume o estado (`Amparius — Offline — 3 pendentes`),
  o que dá ao leitor de tela uma mudança observável.
- **Divergência:** `egui`/AccessKit não oferece regiões “live” equivalentes a
  `aria-live`. Mitigação desta fatia: resumo textual persistente no topo, título
  da janela atualizado e foco levado ao bloco de erro quando ele aparece. A
  paridade plena de anúncios ao leitor de tela pertence à superfície web do
  servidor da loja (ADR 0006), que reutilizará o mesmo contrato de estado.
- Também não há semântica nativa de tabela; a lista é de cartões rotulados, o que
  evita scroll horizontal em 360 px e mantém os rótulos junto dos valores.

## Alternativas descartadas

- **Tauri/WebView2:** melhora acessibilidade web, mas arrasta HTML/CSS/JS e toolchain
  Node para o cliente, contrariando ADR 0001 e a regra clean-room de desenhar
  estado/streaming em Rust. Também aumenta a superfície de dependência do sistema.
- **Slint:** bom rendimento, mas adiciona compilador de UI e restrições de licença
  que precisam de decisão jurídica própria.
- **Iced:** arquitetura Elm viável, porém mais cerimônia para uma fatia de leitura
  e testes de estado menos diretos que o modelo puro adotado aqui.
- **UI web no navegador para a estação:** não atende “binário único” nem operação
  sem navegador; fica reservada à superfície LAN do servidor da loja.

## Consequências

- O cliente passa a abrir janela nativa; `cargo run` exige identidade
  (`tenant`/`node`) e, para sincronizar, relay/credencial. Sem identidade, a
  janela mostra uma tela de configuração com o motivo — falha fechada, sem
  inventar tenant.
- O build do workspace `client` ganha dependências gráficas; o build continua
  offline após o primeiro `cargo fetch`. O teste de renderização usa
  `egui_kittest` sem backend gráfico (somente árvore de UI).
- Boot e primeiro quadro passam a ser medidos e logados
  (`client first frame` com `boot_ms`) para evidência de entrega.
- A resolução de conflito permanece fora do cliente; CL-05 entrega backlog e
  revisão somente leitura. A decisão autorizada exige fluxo posterior próprio.
  CL-02/CL-03 reutilizam a mesma fila e os mesmos tokens. CL-03 projeta as OS
  conhecidas pela fila, envia avanço/pausa/conclusão com `expected_version` e
  chave própria por mutação, bloqueia nova mutação enquanto houver
  pendência/erro/conflito e encaminha conflitos para revisão. A lista local não
  substitui a autoridade do serviço funeral nem resolve entitlement, contrato
  `active` ou transição no cliente.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```
