# Painel do dia do cliente (CL-01)

Guia da tela inicial da estação Amparius. Ela mostra o estado da fila offline da
estação e permite sincronizar com o relay local. Atendimento funeral, ordens de
serviço e revisão de conflito têm telas próprias e ainda não estão nesta versão.

## Antes de abrir

A estação precisa de duas informações:

- **tenant** — a loja/empresa dona da operação;
- **dispositivo** — o nome desta estação (por exemplo `estacao-1`).

Sem elas a janela abre em **Configuração necessária** e explica o que falta. A
tela não cria fila nem inventa identidade.

No PowerShell, a partir da pasta do repositório:

```powershell
$env:AMPARIUS_TENANT = "loja-1"
$env:AMPARIUS_NODE = "estacao-1"
cargo run --manifest-path client/Cargo.toml
```

Para sincronizar com o relay local, configure também:

```powershell
$env:AMPARIUS_RELAY_URL = "http://127.0.0.1:8787"
$env:AMPARIUS_SUBJECT = "operador-1"
$env:AMPARIUS_TOKEN = "<credencial da sessão>"
```

A credencial fica apenas em memória, nunca aparece na tela e nunca é gravada.

## O que a tela mostra

1. **Faixa de conexão** — Online, Offline ou Contingência. A fila local funciona
   nos três; o estado escolhido é gravado na fila. Contingência **não** autoriza
   emissão fiscal.
2. **Fila desta estação** — contadores de pendentes, novas tentativas, conflitos,
   falhas, sincronizadas e revisões abertas.
3. **Sincronização** — mostra se há o que enviar, se está enviando e o resultado
   (sincronizadas, duplicadas, conflitos, rejeitadas, pendentes). Se o relay ou a
   credencial não estiverem configurados, o botão fica desabilitado e o motivo
   aparece — nada é simulado.
4. **Operações** — cada operação com número de sequência, tipo, status, horário
   local, idade, tentativas e código técnico do último erro. Conflito e falha
   aparecem primeiro.
5. **Revisão necessária** — itens que exigem decisão manual. O valor local é
   preservado; a tela não sobrescreve nada.
6. **Rodapé** — botão Atualizar e horário da última sincronização.

## Ações

- **Escolher conexão** (Online/Offline/Contingência): registra o estado real da
  estação. Use Offline quando não houver internet e Contingência apenas quando a
  loja declarar contingência operacional.
- **Sincronizar agora**: envia as operações pendentes em ordem de sequência. Se
  uma falhar por queda de rede, a execução para e a operação continua pendente
  para a próxima tentativa.
- **Atualizar**: relê a fila do disco. Fica bloqueado enquanto uma sincronização
  está em andamento.
- **Conflito ou falha**: abra a tela de sincronização e conflitos (fatia futura)
  para revisar. Não existe botão de "resolver" no painel.

## Quando algo dá errado

- **Não foi possível abrir a fila local**: o bloco mostra o código
  (`corrupt_store`, `unsupported_store_version`, `tenant_mismatch`,
  `node_mismatch`, `io_error`, entre outros), a causa e a próxima ação. Preserve
  o arquivo da fila e acione o suporte; nada é reconstruído automaticamente.
- **Sincronização falhou**: a fila não foi alterada. O código aparece no bloco
  de sincronização; tente novamente depois de corrigir a causa.
- **Sem relay/credencial**: configure as variáveis e reabra a estação.

## Acessibilidade

A tela funciona por teclado, com foco visível e alvos de pelo menos 44 px; todo
estado tem texto e símbolo, não só cor. O título da janela resume o estado
atual. Limite conhecido desta versão: não há anúncio automático equivalente a
`aria-live` para leitores de tela; a superfície web da loja cobrirá esse ponto.

## Referências

- Contrato da tela: [client-day-panel-v1.md](../contracts/client-day-panel-v1.md)
- Fila e reconciliação: [client-offline-state-v1.md](../contracts/client-offline-state-v1.md)
- Decisão de interface: [ADR 0036](../adr/0036-cliente-ui-nativa-egui-painel-dia.md)
