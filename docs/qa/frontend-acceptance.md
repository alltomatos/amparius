# QA — Aceite do frontend Amparius

Este documento valida as primeiras fatias definidas em `docs/adr/0026-frontend-architecture.md`.

## Requisitos comuns

- [ ] Todas as telas apresentam `idle`, `loading`, `empty`, `error` e `success` quando aplicável.
- [ ] A viewport de 360 px não produz scroll horizontal fora de tabelas explicitamente roláveis.
- [ ] Navegação por teclado, foco visível, nomes acessíveis e contraste mínimo WCAG 2.2 AA.
- [ ] Alvos de toque têm pelo menos 44 × 44 px.
- [ ] Nenhum segredo, token, senha, MFA, PII desnecessária ou XML fiscal bruto aparece em log, URL, captura ou erro visual.
- [ ] Cada escrita crítica envia `Idempotency-Key`; cada edição concorrente envia `version`.
- [ ] O erro mostra `code` e `traceId` sem transformar licença, cache expirado ou offline em causas equivalentes.

## CP-01/CP-02 — autenticação e contexto

### CP-A01 — login autorizado

**Dado** um usuário com OIDC/MFA válidos
**Quando** conclui o login
**Então** a sessão é estabelecida sem expor credenciais e o usuário é levado ao contexto permitido.

### CP-A02 — escopo inválido

**Dado** um tenant ou empresa fora do escopo do token
**Quando** a UI tenta selecionar o contexto
**Então** a API rejeita com `TENANT_MISMATCH` ou `PERMISSION_DENIED`, sem confiar em `X-Tenant-Id`.

## CP-05/CP-06 — entitlement

### CP-A03 — concessão idempotente

**Dado** administrador autorizado e empresa do tenant
**Quando** concede um módulo com a mesma chave de idempotência duas vezes
**Então** existe uma única alteração e a auditoria registra o resultado uma vez.

### CP-A04 — concorrência e falha fechada

**Dado** uma licença com `version = N`
**Quando** outro estado já elevou a versão e a UI envia `N`
**Então** a operação falha com `STALE_VERSION`, refaz a leitura e não sobrescreve o estado atual.

### CP-A05 — módulo bloqueado

**Dado** licença inativa, vencida ou quota excedida
**Quando** o usuário acessa a ação do módulo
**Então** a UI mostra o estado específico e não oferece uma ação funcional que o backend recusará.

### CP-A06 — detalhe, auditoria e escrita administrativa

**Dado** um `owner`/`admin` autenticado e uma empresa vinculada ao tenant
**Quando** consulta o detalhe, concede/revoga uma licença ou ajusta quota
**Então** o backend usa somente o tenant/contexto autenticado, exige
`Idempotency-Key` nas escritas, exige `version` no `PATCH`, grava auditoria
append-only e mascara qualquer CNPJ apresentado.

**Evidência:** `docs/contracts/control-plane-entitlement-cp06-v1.md`, testes
`internal/modules/controlplane` e integração `internal/modules/entitlement`
quando `AMPA_CONTROL_POSTGRES_DSN` está configurado.

## CL-01/CL-03 — operação offline

### CL-A01 — fila sem duplicação

**Dado** uma ordem de serviço permitida offline
**Quando** ela é concluída sem rede e depois sincronizada duas vezes
**Então** há uma única operação persistida, com sequência do dispositivo e estado final reconciliado.

### CL-A02 — conflito financeiro/fiscal

**Dado** conflito envolvendo dinheiro ou fiscal
**Quando** a sincronização detecta versões divergentes
**Então** a operação fica pendente de revisão; não é aplicado `last write wins` silencioso.

### CL-A03 — capacidade indisponível offline

**Dado** uma ação que depende de serviço online
**Quando** a rede está indisponível
**Então** a UI explica a indisponibilidade e não simula sucesso.

## CL-02 — atendimento funeral

### CL-A04 — jornada idempotente na fila

**Dado** entitlement funeral ativo, contrato `active` confirmado e referências
opacas válidas
**Quando** o operador cria a jornada duas vezes, em offline ou contingência
**Então** existe uma única operação persistida, com `seq` monotônica e a mesma
`Idempotency-Key`; a tela mostra `queued`, nunca sucesso remoto.

### CL-A05 — bloqueios explícitos

**Dado** entitlement bloqueado, contrato não confirmado ou relay indisponível em
estado online
**Quando** o operador tenta criar/avançar/concluir
**Então** a ação fica bloqueada ou mostra erro técnico específico e nenhuma
operação é adicionada à fila.

### CL-A06 — estados headless

**Dado** o modelo puro de CL-02
**Quando** os estados online, offline, contingência, queued e error são
apresentados
**Então** cada um possui rótulo textual acessível e não contém PII, token ou
payload de atendimento.

### CL-A07 — execução de OS sem duplicação

**Dado** uma OS conhecida localmente e uma versão-base
**Quando** o operador avança, pausa ou conclui em offline/contingência
**Então** a tela lista a OS com `expected_version`, `seq` e estado `queued`, usa
uma chave de idempotência diferente por mutação e a reabertura da fila preserva
a projeção.

### CL-A08 — conflito de OS preservado

**Dado** uma mutação de OS que retorna conflito
**Quando** a reconciliação marca a operação como `conflict`
**Então** a tela mostra código técnico e revisão necessária, bloqueia nova
mutação para a OS e não aplica last-write-wins.

## CL-05 — sincronização e revisão de conflitos

### CL-A09 — backlog e resultado sem dados sensíveis

**Dado** uma fila com pendências, duplicidades, falhas e conflitos
**Quando** o operador abre "Sincronização e revisão"
**Então** a tela lista operação, tipo, `seq`, status e código técnico, mostra
as contagens do último `SyncReport` e não exibe payload, PII, tenant, dispositivo
ou token.

### CL-A10 — retry seguro

**Dado** uma interrupção de transporte com operação ainda `pending`
**Quando** o operador seleciona "Tentar novamente"
**Então** o cliente reapresenta a mesma operação idempotente, sem nova
sequência ou duplicata; falha terminal, conflito e rejeição fiscal não são
reabertos pela UI.

### CL-A11 — revisão sem last-write-wins

**Dado** conflito financeiro ou fiscal com tarefa de revisão aberta
**Quando** o operador seleciona "Abrir revisão"
**Então** a tela abre o detalhe somente leitura, preserva o estado local e não
resolve, fecha ou sobrescreve a operação.

## Evidência mínima do PR

O PR deve anexar comandos reproduzíveis e saída para testes de contrato, acessibilidade/responsividade e fluxo offline. Para CL-05, a evidência mínima inclui os testes headless do backlog e da revisão, além de `cargo test`, `cargo clippy` e `git diff --check`. Sem evidência, a fatia permanece em andamento.

Contrato e decisão da CL-05: `docs/contracts/client-sync-review-v1.md` e
`docs/adr/0037-cliente-sync-review.md`. Guia operacional:
`docs/userguide/cliente-sincronizacao-revisao.md`.
