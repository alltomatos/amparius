# AGENTS.md — Amparius

Regras para qualquer agente de IA (Codex, Claude, OpenCode, Cursor, etc.) que trabalhe neste repositório. Leia antes de editar qualquer arquivo.

## 1. O que é este repositório

ERP modular agêntico, multi-tenant, para funerárias, cemitérios e floriculturas. Financeiro/fiscal é a espinha dorsal. O SaaS roda em Go; o relay de borda e o cliente rodam em Rust; dados em PostgreSQL, cache em Redis, mensageria em RabbitMQ.

O repositório ainda está em **fase de documentação**. Quando o código começar, a estrutura prevista é:

```
cmd/api/            binário HTTP do SaaS
cmd/worker/         consumidores de fila e jobs
internal/modules/   contextos (identity, party, catalog, finance, fiscal, funeral, cemetery, floriculture, inventory, documents, ai, sync)
internal/platform/  banco, fila, cache, auth, observabilidade, erros
migrations/         SQL versionado, forward-only
relay/              workspace Rust do relay de borda
client/             workspace Rust do cliente desktop (fase posterior)
docs/               PRD, arquitetura, DER, UML, ADRs, pesquisa
```

## 2. Regras que não se negociam

1. **Fiscal não se estima.** Alíquota, CST, cClassTrib, NCM, CFOP, código de serviço e vigência vêm de tabela datada e fonte oficial. Se a regra não está na tabela, a operação **falha** — nunca chuta, nunca "aproxima".
2. **Isolamento por tenant.** Toda tabela de negócio tem `tenant_id`; toda query passa por RLS. Nenhum código roda sem `tenant_id` no contexto.
3. **Idempotência em tudo que emite documento fiscal ou move dinheiro.** Chave de idempotência persistida; repetir não duplica.
4. **A operação não para.** Fluxos críticos (venda, atendimento, caixa, emissão em contingência) precisam funcionar no relay sem internet. O que não puder funcionar offline deve dizer isso explicitamente na UI.
5. **Dado pessoal é dado sensível.** LGPD vale para clientes, familiares e falecidos. Não logar PII; mascarar em prompts de IA; retenção definida.
6. **Nenhum segredo no repositório.** Certificados, chaves e tokens ficam em cofre/variável de ambiente; `.env` nunca é commitado.
7. **Mudança estrutural exige ADR.** Novo módulo, quebra de contrato, mudança de modelo de dados ou de topologia → `docs/adr/` antes do código.
8. **Migração é forward-only.** Nunca editar migração já aplicada; correção vira nova migração.

## 3. Convenções de código

### Go

- `context.Context` como primeiro parâmetro de tudo que faz I/O; sem `context.TODO` em produção.
- Erros explícitos, com `errors.Is/As`; nada de `panic` fora de bootstrap.
- Dinheiro em `int64` (centavos) ou `NUMERIC(18,2)` no banco; nunca `float`.
- Tempo em UTC no banco; conversão para America/Sao_Paulo só na borda de apresentação.
- Sem estado global mutável; dependências injetadas no construtor.
- SQL explícito (pgx/sqlc); sem ORM mágico em caminho fiscal.
- IDs opacos (`uuidv7` ou ULID), nunca sequenciais expostos.

### Rust

- `edition = "2024"` quando o toolchain permitir; MSRV documentada.
- `thiserror` para erros de biblioteca, `anyhow` só em binário.
- `unsafe` proibido sem ADR e comentário de segurança.
- Relay: `tokio`, `sqlx`/`tokio-postgres`, `serde`; binário único, sem dependência de runtime externo.
- Toda chamada de rede com timeout, retry limitado e jitter.

## 4. Fronteiras entre módulos

- Cada módulo tem **schema próprio** (`finance.*`, `fiscal.*`, `funeral.*`).
- Um módulo **não lê tabela de outro**. Comunicação por:
  - chamada de serviço público do módulo, ou
  - evento publicado no RabbitMQ (`<module>.<entity>.<action>`).
- IDs são referências opacas; `JOIN` entre schemas é proibido em código de módulo (views de leitura podem existir em `reporting`).
- Contratos de evento são versionados (`v1`, `v2`); campo novo é opcional, remoção exige nova versão.

## 5. Testes e evidência

- Unidade para regra de negócio; integração com PostgreSQL real (testcontainers) para repositório.
- **Golden files** para XML fiscal: entrada, XML gerado, resposta da SEFAZ/prefeitura simulada.
- Teste de contrato para cada evento publicado e para cada endpoint público.
- Teste de caos do relay: queda de rede no meio da emissão, reconexão, reconciliação.
- Performance: benchmark para listagens e cálculo fiscal; orçamento de latência no PR.
- Nenhum PR é "pronto" sem comando reproduzível e saída colada.

## 6. Fiscal — regras específicas

- Tabelas de tributação têm `valid_from` e `valid_to`; consulta sempre pela data de competência.
- XML gerado é validado contra XSD oficial antes de transmitir.
- Resposta da SEFAZ/prefeitura é persistida crua (XML + hash) e nunca sobrescrita.
- Contingência: marcar o documento com modo de contingência, prazo de transmissão e reconciliar depois.
- Cancelamento e substituição seguem o prazo legal; o sistema bloqueia fora do prazo.
- Reforma tributária (IBS/CBS/IS): o motor precisa suportar RTC com tabelas oficiais. Ver `docs/research/receita-federal.md`.

## 7. IA e MCP

- Todo agente declara: tenant, papel, tools permitidas, escopo de dados e limite de custo.
- Padrão é **somente leitura**; qualquer escrita exige permissão explícita e confirmação do usuário.
- Chamadas de ferramenta são auditadas (quem, o quê, entrada, saída, custo).
- PII é mascarada antes de sair para o provedor de modelo; prompt e resposta ficam versionados.
- Servidores MCP externos entram por allowlist do tenant; nada de acesso a rede aberta por padrão.

## 8. Offline e sincronização

- O relay mantém um **log de operações** com sequência monotônica por dispositivo.
- Conflito: dados mestres são autoritativos no servidor; operações locais são reaplicadas por idempotência.
- Toda entidade sincronizável tem `updated_at`, `version` e origem (`origin_node`).
- Nada de "last write wins" silencioso em dinheiro ou fiscal: conflito é registrado e revisado.

## 9. Definição de pronto

Uma entrega só termina quando:

1. critério de aceite testado (unitário e/ou integração);
2. migração revisada e reversível por nova migração;
3. evento/documento versionado e documentado;
4. observabilidade mínima (log estruturado, métrica, trace);
5. impacto de segurança/LGPD avaliado;
6. documentação atualizada (PRD, DER ou ADR, conforme o caso);
7. comando de verificação e saída anexados ao PR.

## 10. Commits e PRs

- Mensagem no imperativo, escopo do módulo: `feat(fiscal): adiciona cálculo de IBS`.
- Um PR por assunto; sem misturar refactor com feature.
- Nunca commitar segredo, dump de produção ou dado pessoal.
- PR descreve: problema, decisão, evidência, risco e rollback.
