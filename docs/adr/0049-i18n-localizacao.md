# ADR 0049 — Internacionalização (pt-BR, pt-PT, es-419)

Status: proposto · 28/11/2026

## Contexto

O Amparius era pensado só para o mercado brasileiro. Ronaldo decidiu expandir
para Portugal e para mercado(s) de língua espanhola, o que exige suporte
multilíngue na interface (web, painel SaaS/manager e cliente desktop Rust),
nos textos de erro e nas comunicações (e-mail, documentos não fiscais).

Isso **não** afeta o núcleo fiscal: NFS-e, NF-e, DANFE, DPS e os XMLs oficiais
continuam em português do Brasil, por obrigação legal (ver AGENTS.md §2.1 e §6,
ADR 0010, ADR 0031). i18n é uma camada de interface e mensageria, nunca do
documento fiscal.

Este ADR cobre só **desenho**: locale, onde mora a preferência, catálogo de
mensagens no backend Go, formato de catálogo nos três clientes, fronteira com
o fiscal, estratégia para conteúdo cadastrado pelo usuário, impacto no
offline/relay, e processo de tradução. Não há código de produção nesta fatia.

**Decisões de Ronaldo (28/11/2026)**, já incorporadas abaixo e não mais em
aberto: (1) espanhol é `es-419` (neutro latino-americano), não `es-ES`;
(2) o escopo desta fase é **somente tradução de interface/mensagens** —
nenhuma mudança fiscal, nenhuma moeda ou fiscalidade de Portugal, nenhum
documento fiscal fora de `pt-BR`; `pt-PT` é só idioma, moeda e regras fiscais
não derivam do locale.

## Decisão

### 1. Locales suportados e fallback

- Locales suportados nesta fatia: `pt-BR`, `pt-PT`, `es-419`.
- `pt-BR` é o locale padrão do sistema e o fallback final de qualquer chave
  ausente, em qualquer cliente e no backend. Nunca falha fechado por chave de
  UI ausente (chave ausente faz fallback e **loga aviso**, nunca derruba a
  tela); fiscal falha fechado por regra ausente continua valendo (AGENTS.md
  §1), são domínios diferentes.
- **Espanhol: `es-419` (neutro latino-americano)**, decisão do Ronaldo
  (28/11/2026). Catálogo continua versionando por região caso apareça no
  futuro um país específico com variação relevante (`es-MX`, `es-AR` etc.);
  isso não exige refazer chave, só acrescentar um novo arquivo de locale.
- Cadeia de fallback: `pt-PT → pt-BR`; `es-419 → pt-BR` (não há "es genérico"
  intermediário nesta fatia). `pt-PT` não cai em `es-419` nem vice-versa.
- **Chave de tradução é um id estável, nunca o texto-fonte**: formato
  `modulo.tela.elemento` (ex.: `finance.cash_session.open_button`,
  `errors.AUTH.INVALID_CREDENTIALS`). Texto em português não é chave; isso
  evita que uma correção de copy em pt-BR quebre pt-PT/es silenciosamente.

### 2. Onde mora o idioma

- **Preferência do usuário** (`identity.app_user`, nova coluna
  `locale text NOT NULL DEFAULT 'pt-BR'` com `CHECK` na lista de locales
  suportados) é a fonte de maior prioridade quando o usuário está
  autenticado. Editável no perfil do próprio usuário.
- **Padrão por empresa/tenant** (nova coluna `locale` em `identity.company`,
  mesmo `DEFAULT`/`CHECK`) é o fallback para usuário sem preferência
  explícita e para telas pré-login associadas a uma empresa conhecida (ex.:
  link de convite com `company_id`).
- **`Accept-Language`** do navegador é usado só como sugestão na tela de
  login/onboarding, antes de existir usuário ou empresa resolvida — nunca
  sobrescreve preferência já salva, nunca decide locale de e-mail/documento
  já gerado.
- Ordem de resolução no backend: `usuário autenticado.locale` →
  `empresa.locale` → `Accept-Language` (login/onboarding apenas) → `pt-BR`.
- O locale resolvido entra no **contexto de requisição junto do `tenant_id`**
  (mesmo padrão de propagação de AGENTS.md §2): middleware HTTP popula
  `ctx.Locale` do mesmo jeito que popula `ctx.TenantID`, nunca lido de novo
  do banco em cada handler.
- RLS não muda: `locale` é metadado de interface, não cruza tenant.

### 3. Backend Go — erros e mensagens

- Toda resposta de erro de API mantém **código estável** (ex.:
  `INVALID_CREDENTIALS`, `TENANT_NOT_FOUND`) já em uso nos contratos
  existentes (ex. `docs/contracts/auth-login-v1.md`) — isso não muda.
  Acrescenta-se um campo `message` **localizado** no locale resolvido da
  requisição, só para exibição; nenhum cliente decide lógica pelo texto de
  `message`, somente pelo `code`.
- Catálogo de mensagens do backend: arquivos `locales/<locale>/errors.json`
  versionados no repositório Go (`internal/platform/i18n` na fase de
  código), carregados em memória no boot; sem I/O de tradução em runtime.
- **Nenhum texto traduzido é persistido em dado de negócio.** Nome de
  produto do catálogo, descrição de contrato, observação de atendimento
  ficam exatamente como o usuário digitou, no idioma que o usuário digitou.
  i18n de sistema nunca sobrescreve dado digitado.
- E-mails e documentos não fiscais (recibo interno, comprovante, convite)
  são renderizados no locale do destinatário (usuário/empresa), com o mesmo
  catálogo; corpo do e-mail nunca mistura dois locales.

### 4. Clientes (web, manager, desktop Rust)

- Formato de catálogo: **JSON por locale com sintaxe ICU MessageFormat**
  para plural/gênero/interpolação (`"{count, plural, one {# título} other
  {# títulos}}"`), mesmo formato nos três clientes para permitir catálogo
  único versionado e sincronizado.
- Pluralização, data, número e moeda usam `Intl` no web/manager
  (JS/TS) e crate `icu4x` (ou equivalente MSRV-compatível, a confirmar na
  fatia de código) no desktop Rust — nunca formatação manual de string.
- **Moeda do documento nunca é derivada do idioma da interface.** BRL/EUR (e
  qualquer moeda futura) vêm do campo de moeda do registro financeiro
  (`AGENTS.md` §3: dinheiro em `int64`/`NUMERIC`, nunca `float`); o locale só
  decide o separador decimal/agrupamento e o símbolo de exibição, nunca o
  valor numérico nem qual moeda é essa.
- Assistente de IA (`docs/design/AI-ASSISTANT.md` §2) já carrega `locale` no
  envelope de contexto; esta fatia só formaliza que o valor vem da mesma
  resolução do item 2 (usuário → empresa → `pt-BR`) e que o catálogo de
  `intents`/`suggestions` é traduzido como qualquer outra chave de UI —
  prompt/resposta do modelo seguem mascarados por PII independente do
  locale (AGENTS.md §7).

### 5. Fronteira com o fiscal

- NFS-e, NF-e, DANFE, DPS e XML oficiais (DPS v1.01, leiaute nacional —
  ADR 0010, ADR 0031) **permanecem inteiramente em português do Brasil**,
  incluindo rótulo impresso do DANFE/representação visual do documento
  fiscal. i18n não traduz nome de tributo, código de serviço, CFOP ou
  qualquer termo do leiaute oficial.
- A **tela** que envolve o documento fiscal (botões, status, mensagens de
  erro de emissão) pode ser traduzida; o **documento em si** nunca.
- Termos fiscais que aparecem fora do XML (ex.: rótulo "ISS" num resumo de
  venda) usam um **glossário controlado** por módulo fiscal: ou ficam sempre
  em português (recomendado, por ser termo legal brasileiro sem tradução
  correspondente em PT-PT/ES) ou recebem nota explicativa traduzida ao lado
  do termo técnico intacto — a decisão fica na Lacuna 1.
- Nenhum valor fiscal (alíquota, prazo, CST) é "estimado" para exibir em
  outro idioma; a fronteira de AGENTS.md §2.1 vale também aqui.

### 6. Conteúdo cadastrado pelo usuário

- Catálogo de produtos/serviços, textos de ajuda contextual e conteúdo do
  Userguide, e system prompts do assistente de IA são tratados por
  categoria, não por regra única:
  - **Catálogo de produtos/serviços do tenant**: texto digitado pelo
    próprio tenant; **não traduzido automaticamente**. Cada empresa opera
    no(s) locale(s) que atende; se quiser multilíngue no próprio catálogo,
    cadastra campos por locale (schema já suporta `jsonb`/colunas
    adicionais na fase de código, fora do escopo deste ADR).
  - **Ajuda contextual e Userguide**: conteúdo do produto, mantido pelo bot
    de Documentação; segue o mesmo catálogo de chaves do item 1, com
    fallback para `pt-BR` quando uma página ainda não foi traduzida.
  - **System prompt do assistente de IA**: varia por locale (idioma de
    resposta do assistente = locale resolvido do usuário, item 2); o
    *conteúdo* que o assistente lê (registros do tenant) nunca é traduzido
    antes de entrar no prompt — só a instrução/comportamento do assistente
    muda de idioma.

### 7. Offline / relay

- Catálogos de tradução (JSON ICU) são **empacotados no cliente** (desktop e
  relay), nunca buscados em runtime; isso preserva "a operação não para"
  (AGENTS.md §2.4) mesmo sem rede.
- Catálogo carrega uma **versão** própria (`catalog_version`, semver),
  independente da versão do binário, para permitir atualizar textos sem
  recompilar; o relay valida compatibilidade de versão do catálogo no
  startup e usa o catálogo embutido mais recente disponível localmente se o
  servidor estiver inacessível.
- O **snapshot assinado** do relay (ADR 0022; ADR 0046, número a confirmar no merge) nunca carrega texto
  traduzido: carrega dado estruturado (códigos, ids, valores); a tradução
  acontece inteiramente no cliente, a partir do catálogo local. Isso evita
  que o snapshot cresça com N cópias de string por locale e mantém a
  assinatura independente de locale.

### 8. Processo e qualidade

- **Glossário controlado** pt-BR/pt-PT/es-419: termos de produto (ex. "plano
  de pagamento", "atendimento", "sepultamento"/"sepultura" vs. equivalentes
  em PT-PT e ES) mantido pelo bot de Documentação, revisado por falante
  nativo de cada locale antes de publicar.
- **Revisão por falante nativo** é obrigatória para `pt-PT` e `es-419` antes
  de qualquer fatia ir a produção; tradução automática só como rascunho
  interno, nunca publicada sem revisão humana.
- **Teste de cobertura de chaves** no CI: falha o build se um catálogo
  não-`pt-BR` tiver chave ausente presente em `pt-BR` (chave extra é só
  aviso); roda por cliente (web, manager, desktop) e no backend Go.
- **Pseudo-localização** (`pt-BR` → `[pt-BR: texto]` expandido) usada em
  ambiente de desenvolvimento para pegar texto hardcoded fora do catálogo e
  estouro de layout por string mais longa (PT-PT e ES tendem a ser mais
  longos que PT-BR).
- Fatias verticais sugeridas, em ordem:
  1. **I18N-01B (backend Go)**: catálogo de erros, middleware de resolução
     de locale no contexto de requisição, coluna `locale` em `app_user` e
     `company` (migration forward-only), teste de cobertura de chaves no
     CI do backend.
  2. **I18N-01C (web + manager)**: catálogo ICU, seletor de locale persistido
     via preferência de usuário, formatação de data/número/moeda por
     `Intl`, teste de cobertura de chaves no CI do frontend.
  3. **I18N-01D (desktop Rust)**: catálogo empacotado + versão, formatação
     via crate ICU, teste de cobertura de chaves no CI do desktop, prova de
     que o catálogo funciona sem rede.
  4. **I18N-01E (documentação/Userguide)**: glossário publicado, processo
     de revisão por falante nativo documentado, primeira leva de páginas do
     Userguide traduzidas com fallback.

## Consequências

- Toda tela nova passa a exigir chave de catálogo desde o primeiro commit;
  texto hardcoded passa a falhar o teste de cobertura assim que
  I18N-01B/C/D estiverem no CI.
- `identity.app_user` e `identity.company` ganham uma coluna cada
  (migration forward-only, sem impacto em RLS existente).
- Documento fiscal continua 100% português do Brasil; nenhuma regra fiscal
  muda.
- O relay/desktop ganham mais um artefato versionado (catálogo) para
  empacotar e validar no startup, junto do que já faz com snapshot e chave
  de assinatura.

## Alternativas descartadas

- **Texto como chave de tradução** (`t("Entrar")` em vez de
  `t("auth.login.submit_button")`): descartado porque qualquer correção de
  copy em pt-BR quebraria silenciosamente pt-PT/es (a chave "é" o texto
  antigo). Chave estável e opaca evita isso.
- **`es-ES` (ibérico) ou um `es-XX` específico de país como primeiro locale
  espanhol**: descartado por decisão do Ronaldo (28/11/2026); `es-419`
  neutro latino-americano é o locale espanhol desta fase.
- **Fiscalidade ou moeda portuguesa (IVA, NIF, fatura-recibo) para `pt-PT`**:
  descartado por decisão do Ronaldo; `pt-PT` é só idioma de interface sobre
  o mesmo motor fiscal e a mesma moeda do registro, nunca derivados do
  locale.
- **Tradução automática de catálogo/produto do tenant**: descartado; dado
  de negócio digitado pelo usuário não é propriedade do sistema para
  traduzir, e teria custo/risco de IA sem necessidade comprovada nesta
  fase.
- **Buscar catálogo de tradução em runtime (CDN/API)**: descartado para o
  relay/desktop por violar AGENTS.md §2.4 (operação não para sem rede);
  também descartado para o backend Go por adicionar I/O e falha evitável a
  um caminho crítico.

## Critérios de aceite

1. ADR e `docs/contracts/i18n-v1.md` cobrem os 9 pontos do pedido do
   Ronaldo (locale/fallback, onde mora o idioma, backend, clientes, fiscal,
   conteúdo do usuário, offline/relay, processo, lacunas), cada um com
   recomendação e justificativa.
2. Nenhuma chave de tradução é texto-fonte; convenção `modulo.tela.elemento`
   documentada e exemplificada.
3. Documento fiscal (NFS-e/NF-e/DANFE/DPS/XML) permanece fora do escopo de
   tradução, explicitado tanto no ADR quanto no contrato.
4. Moeda do documento provadamente não depende do locale de interface
   (exemplo concreto no contrato: BRL exibido em `pt-PT` sem virar EUR).
5. Offline: catálogo empacotado e versionado, sem dependência de rede,
   snapshot assinado sem texto traduzido — critério testável descrito no
   contrato (fatia I18N-01D).
6. Teste de cobertura de chaves descrito como gate de CI (falha build por
   chave ausente em locale não-padrão), um critério por cliente/backend.
7. Fatias I18N-01B/C/D/E com critério de aceite testável cada uma.
8. Lista única de lacunas/decisões que dependem do Ronaldo, cada uma com
   recomendação prévia.
9. `git diff --check` = 0; numeração de ADR livre confirmada em `origin/main`
   e nas branches abertas no momento da escrita (sem colisão como a de
   0045/0046).

## Lacunas e decisões abertas

Bloco único; cada item com a recomendação do arquiteto, aguardando decisão
do Ronaldo. **Resolvidas em 28/11/2026** (fora desta lista; incorporadas em
Contexto e nas seções 1 e 5): espanhol `es-419`; `pt-PT` só idioma, sem
moeda/fiscalidade portuguesa.

1. **Glossário de termos fiscais fora do XML** (ex. "ISS", "DANFE") em
   `pt-PT`/`es-419`: manter sempre em português (recomendado) ou glosar com
   nota traduzida ao lado do termo técnico.
2. **Catálogo multilíngue de produto/serviço do tenant** (item 6): confirmar
   que não é objetivo desta fase — cada empresa opera no locale que atende,
   sem campo de tradução por item de catálogo. Se for objetivo, é fatia
   adicional (schema), não parte de I18N-01B/C/D.
3. **Formato de telefone/documento por país** (CPF/CNPJ assume Brasil; PT
   usa NIF, ES usa NIF/CIF/DNI): fora do escopo deste ADR (é cadastro de
   parceiro, não i18n de texto), mas registrado aqui porque vai aparecer na
   primeira tela de cadastro em `pt-PT`/`es-419`. Recomendo tratar como ADR
   específico de `party` quando houver cliente real desses países, não
   bloquear esta fatia de i18n por isso.
4. **Fuso horário por locale/país**: AGENTS.md §3 já fixa UTC no banco e
   `America/Sao_Paulo` na borda de apresentação brasileira; com Portugal e
   possível América espanhola, a borda de apresentação precisa de fuso por
   usuário/empresa, não um fuso fixo. Recomendo acrescentar coluna de fuso
   horário no mesmo lugar da coluna `locale` (item 2) nesta mesma fatia
   I18N-01B, em vez de abrir ADR separado — avisar se Ronaldo preferir
   tratar isso fora do i18n.
