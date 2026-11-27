# ADR 0048 — Emissor próprio de credencial: login, sessão, revogação e escopo tenant×empresa

Status: proposto · 27/11/2026 · AUTH-01A (issue #103) · referencia ADRs 0007,
0022, 0024, 0030, 0038, 0040, 0047

## Contexto

O ADR 0024 (HARDEN-01A) já entregou a fronteira de autorização de produção:
`auth.OIDCVerifier`, `auth.MFAVerifier`, `auth.RolePolicy`, `auth.Require`,
`identity.Repository.ResolveSubject`, `identity.user_company` e o pacote
`auth.PlatformService`/`RequirePlatform` (ADR 0040) para `/admin/v1`. O que
não existe é quem emite e valida a credencial: não há provedor OIDC
configurado, nenhum usuário tem senha, e o ADR 0038 registra o bloqueio atual
— `cmd/api` não injeta `Authenticator`, então toda rota de produção responde
`503 AUTHENTICATION_UNAVAILABLE` por falha fechada. O usuário criado pelo
onboarding (`migrations/0009_identity_onboarding.sql`) fica `pending_auth` e
não tem como se autenticar.

Pedido de Ronaldo em 27/11/2026: painel SaaS (control plane/manager), cliente
web e cliente desktop Rust exigem login, com RLS e permissões. OIDC externo e
MFA seguem **adiados por decisão do usuário, mas somente para o login comum
de tenant** (clientes web/desktop e painel SaaS do cliente) — não há
orçamento/prioridade para integrar um IdP agora nessa camada. MFA de
`platform_admin` **não está incluído nesse adiamento** (ver §9): já é
obrigatório por ADR 0040 e pelo código vigente, antes mesmo deste ADR existir.
A costura (`OIDCVerifier`, `MFAVerifier`, `PlatformVerifier`) permanece: ela
foi desenhada para ser agnóstica de provedor, então um emissor próprio pode
implementá-la sem reabrir o ADR 0024.

Revisão de 27/11/2026 (parecer do Analista de Sistemas sobre a branch
`docs/auth-01a-login`, commit `e4d8635`): acrescenta a seção 5 (resolução de
tenant no login, lacuna bloqueante identificada no parecer) e explicita em §9
que o MFA de plataforma é inegociável. Sem essas duas correções o desenho não
era implementável como estava — `app.amparius.com` é domínio único e o
handler de login não tinha como descobrir o tenant antes de ler
`identity.app_user` sob RLS.

Este ADR é **somente desenho**: nenhum código de produção é alterado aqui.
AUTH-01B implementa.

## Decisão

### 1. Emissor próprio, mesma costura

O Amparius passa a ser seu próprio "provedor OIDC" para efeito de contrato: um
serviço interno (`internal/platform/auth/issuer` ou adaptador em
`internal/modules/identity`) implementa `auth.OIDCVerifier` (tenant) e
`auth.PlatformVerifier` (plataforma) validando um JWT assinado pelo próprio
Amparius em vez de delegar a um IdP externo. **Nenhuma interface do ADR 0024
muda**: `auth.Service`, `auth.Require`, `auth.RolePolicy`,
`identity.ResolveSubject` e `identity.user_company` são reaproveitados como
estão. Isso confirma, na prática, que a costura do ADR 0024 era
provedor-agnóstica, como prometido.

Dois emissores completamente separados, sem chave ou tabela compartilhada:

- **Emissor de tenant** (`app.amparius.com`): `iss = "amparius-saas"`,
  credenciais em `identity.*` (banco da célula, sob RLS por tenant).
- **Emissor de plataforma** (`manager.amparius.com`, ADR 0040): `iss =
  "amparius-platform"`, `aud = "manager"`, credenciais em `platform.*` no
  **control plane** — nunca na célula, porque `platform_admin` não pertence a
  nenhum tenant (ADR 0040 decisão 1).

### 2. Modelo de credencial e armazenamento

**Tenant (banco da célula, schema `identity`):**

- `identity.app_user` ganha `password_hash text`, `password_algo text`
  (`'argon2id:v1'`, versionado para permitir reforço de parâmetros sem
  reescrever o contrato), `password_updated_at timestamptz`,
  `failed_attempts int NOT NULL DEFAULT 0`, `locked_until timestamptz`.
  Nenhuma coluna nova é lida sem RLS; todas pertencem à mesma tabela já
  isolada por tenant.
- `identity.refresh_token`: `id`, `tenant_id`, `user_id`, `family_id` (agrupa
  a cadeia de rotação), `token_hash` (SHA-256, não o token cru — mesmo padrão
  do cache de autenticação do relay, ADR 0022 §2), `issued_at`, `expires_at`,
  `rotated_at`, `revoked_at`, `revoked_reason`. RLS por `tenant_id`.
- `identity.login_audit`: `id`, `tenant_id`, `user_id`, `result`
  (`success|invalid_credentials|locked|password_expired_policy|mfa_required`),
  `at`. Sem e-mail, sem IP crú, sem token, sem senha — ver §10.
- `identity.credential_token`: tabela única para o token de uso único de
  **bootstrap** e de **recuperação de senha** (§12/§13): `id`, `tenant_id`,
  `user_id`, `token_hash`, `purpose` (`bootstrap|recovery`), `expires_at`,
  `consumed_at`.

**GRANT por coluna (AGENTS.md §4, mesmo padrão do FIN-02A/#99):**
`amparius_app` já tem `GRANT SELECT` em `identity.app_user` desde a migration
0009. A migration de AUTH-01B (próximo número livre em `migrations/`, hoje
`0041`) adiciona:

```sql
GRANT UPDATE (password_hash, password_algo, password_updated_at,
    failed_attempts, locked_until, status, auth_subject)
    ON identity.app_user TO amparius_app;
```

Nenhum `UPDATE` amplo na linha; a aplicação nunca ganha permissão de alterar
`email`, `tenant_id` ou `name` pelo caminho de autenticação.

**Plataforma (control plane, schema novo `platform`, mesma lógica do
`entitlement` do ADR 0038):** `platform.admin_user` (`id`, `email`,
`password_hash`, `password_algo`, `totp_enrolled boolean`, `status`,
`created_at`), `platform.admin_refresh_token` e `platform.admin_login_audit`
— réplicas estruturais das tabelas de tenant, mas vivendo no control plane
porque `platform_admin` não é RLS-isolável por tenant (não tem tenant).

### 3. Hash de senha

`argon2id` com parâmetros versionados em `password_algo` (permite subir custo
depois sem migrar dado: ao validar, se `password_algo` for uma versão antiga
mas a senha estiver correta, o backend re-hasheia com os parâmetros atuais no
mesmo UPDATE). Parâmetros iniciais propostos: memória 19 MiB, iterações 2,
paralelismo 1 (baseline OWASP atual) — ajustável sem ADR, pois é parâmetro
operacional, não arquitetural.

### 4. Tokens

- **Access token**: JWT `EdDSA` (Ed25519), `kid` no header identifica a chave
  ativa. Claims: `iss`, `sub` (= `identity.app_user.id`, nunca e-mail),
  `tenant_id`, `company_id`, `roles`, `iat`, `exp`, `jti`. TTL proposto:
  **10 minutos**. O emissor de plataforma usa claims equivalentes sem
  `tenant_id`/`company_id` (ADR 0040 decisão 1: plataforma não carrega
  escopo de tenant) e `aud = "manager"`.
- **Refresh token**: opaco, 256 bits aleatórios, nunca JWT. O cliente guarda o
  valor cru; o servidor guarda só `SHA-256(valor)` — o mesmo princípio do
  cache de autenticação do relay (ADR 0022 §2: "o token bruto nunca é
  serializado"). TTL proposto: 30 dias, renovado a cada rotação (janela
  deslizante) até logout/revogação explícita.
- **Chave de assinatura**: referência opaca ao cofre/KMS (`key_id`), nunca em
  banco ou log — mesma regra do ADR 0024 (nada de segredo em log) e do padrão
  de certificado do `docs/ENGINEERING.md` (§ "Certificados nunca armazenam
  senha..."). Rotação por `kid`: o verificador aceita a chave pública do `kid`
  corrente e da anterior durante a janela de sobreposição, nunca uma chave
  desconhecida.

### 5. Resolução de tenant no login

O login de tenant (`POST /v1/auth/login`) recebe `{email, password,
companyId}` sem `tenantId`: `app.amparius.com` é domínio único e o usuário
não sabe (nem deve saber) o identificador do próprio tenant. Isso colide com
a regra de `AGENTS.md` de que nenhum código roda sem `app.tenant_id` no
contexto — e com o schema atual, que só garante `UNIQUE (tenant_id, email)`
(`migrations/0009_identity_onboarding.sql:19-20`), nunca `UNIQUE(email)`
global. O handler precisa descobrir o tenant **antes** de poder usar o
caminho normal protegido por RLS.

**Mecanismo:** uma função `SECURITY DEFINER` dedicada e mínima,
`identity.resolve_login_candidates(p_email text)`, no mesmo padrão já usado
por `identity.create_onboarding` (mesma migration 0009): dono é o papel de
migração, que ignora RLS por ser dono das tabelas (RLS não é forçada sobre o
dono por padrão) — não é necessário `BYPASSRLS` na role da aplicação, e esta
**continua sem esse atributo**. A função devolve só o mínimo necessário para
autenticar, nunca `email`, `name` ou qualquer outra coluna:

```sql
CREATE FUNCTION identity.resolve_login_candidates(p_email text)
RETURNS TABLE (
    tenant_id uuid, user_id uuid, password_hash text, password_algo text,
    status text, failed_attempts int, locked_until timestamptz
)
LANGUAGE sql SECURITY DEFINER SET search_path = identity, public
AS $$
    SELECT tenant_id, id, password_hash, password_algo, status,
           failed_attempts, locked_until
      FROM identity.app_user
     WHERE email = lower(btrim(p_email));
$$;

REVOKE ALL ON FUNCTION identity.resolve_login_candidates(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.resolve_login_candidates(text) TO amparius_app;
```

`amparius_app` não ganha `GRANT SELECT` ampliado em `identity.app_user` por
e-mail — continua lendo a tabela só sob RLS, como hoje. Nenhum papel da
aplicação ganha `BYPASSRLS`.

**Fluxo:**

1. 0 linhas (e-mail não existe em nenhum tenant): o handler ainda executa uma
   verificação `argon2id` contra um hash fixo de "isca" (constante de
   configuração, nunca um hash real) para manter o custo de CPU parecido com
   o caso de senha errada, e responde `401 INVALID_CREDENTIALS`.
2. 1 linha: fluxo normal — verifica a senha contra esse hash; se correta,
   `SET app.tenant_id` para esse tenant e segue pelo caminho já existente
   (bloqueio, vínculo com `companyId` via `identity.user_company`, emissão de
   token). Caso comum (>99% dos logins).
3. N > 1 linhas (mesmo e-mail em tenants diferentes — permitido pelo schema,
   já que a unicidade é por tenant): o handler verifica a senha contra cada
   hash candidato, em ordem, até achar exatamente um que bata; autentica
   nesse tenant sem nunca responder ao cliente quais ou quantos tenants
   existem para aquele e-mail. Zero ou mais de um candidato batendo (senha
   igual reaproveitada em contas distintas) responde o mesmo
   `401 INVALID_CREDENTIALS` genérico.

**Anti-enumeração:** a resposta e o código HTTP são idênticos para e-mail
inexistente, senha errada e qualquer condição de resolução — `ACCOUNT_LOCKED`/
`ACCOUNT_PENDING_AUTH` só aparecem **depois** que exatamente um tenant foi
resolvido por senha correta, nunca antes. O tempo de resposta não é
matematicamente constante (o caso N>1 roda mais de uma verificação
`argon2id`), mas N>1 é raro e pequeno na prática, então não é um oráculo
confiável de enumeração — risco residual registrado, não eliminado por
desenho.

**Rate-limit:** além do `failed_attempts` por usuário (que só existe depois
que um tenant foi resolvido), um rate-limit por `IP + e-mail` normalizado
(Redis, sliding window, mesma defesa em profundidade do §10) cobre o caso de
e-mail inexistente, que não tem contador em banco.

**Impacto no contrato:** `POST /v1/auth/login` ganha `tenantHint` **opcional**
(slug/código curto do tenant), usado só quando o chamador já sabe o tenant
(ex.: link de convite/onboarding que fixa o tenant explicitamente, §12) para
pular a resolução por e-mail; ausente na maioria das chamadas. Convite
(`/v1/auth/invite`) e bootstrap já fixam `tenant_id` pelo contexto autenticado
de quem convida — só o login "frio" precisa de resolução por e-mail.

**Critério de aceite de teste (PostgreSQL descartável, AUTH-01B/C):** com
dois tenants e o mesmo e-mail em ambos, (a) `identity.resolve_login_candidates`
devolve exatamente 2 linhas; (b) o papel `amparius_app` não tem `BYPASSRLS`
(`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'amparius_app'` é falso) e
um `SELECT` direto em `identity.app_user` sem `app.tenant_id` configurado
continua devolvendo 0 linhas; (c) a função nunca expõe `email` ou `name`; (d)
login com a senha certa autentica no tenant certo sem o cliente nunca ver
quantos tenants existiam para aquele e-mail.

### 6. Endpoints (tenant)

`POST /v1/auth/login`, `POST /v1/auth/refresh`, `POST /v1/auth/logout`,
`POST /v1/auth/revoke` (revoga todas as sessões do usuário — uso
administrativo/self-service em caso de suspeita de comprometimento). Contrato
completo em `docs/contracts/auth-login-v1.md`.

### 7. Sessão, rotação e revogação

Rotação de refresh token com **detecção de reuso** (igual ao padrão
recomendado pela OWASP para refresh rotativo, já citado no pedido de
Ronaldo): cada `refresh` consome o token atual e emite um novo na mesma
`family_id`; usar um token já rotacionado revoga **toda a família** e grava
`login_audit` com `result=refresh_reuse_detected` — tratado como possível
token roubado, não como erro de concorrência.

`logout` revoga só o refresh token da sessão atual. `revoke` (autenticado,
exige a própria senha ou é disparado pelo admin SaaS) revoga todas as famílias
do usuário. Access tokens não entram em blocklist nesta fatia — a defesa é o
TTL curto (10 min); revogação "imediata" de access token (ex.: desligamento de
funcionário) depende de um blocklist com TTL ≤ TTL do access token e é
**decisão aberta** (§ Lacunas, item 4).

### 8. Escopo tenant×empresa

Login exige `companyId` explícito no corpo (não existe "sessão sem empresa"):
validado contra `identity.user_company` antes de emitir o primeiro token,
igual ao que `auth.Service.Authenticate` já faz hoje para token OIDC. Troca de
empresa **não exige novo login**: `POST /v1/auth/refresh` aceita
`companyId` opcional; o servidor valida o novo `companyId` contra
`identity.user_company` e emite um access token para a nova empresa mantendo
o mesmo refresh token (mesma família). Isso cobre "escopo tenant×empresa"
sem introduzir um terceiro tipo de token.

### 9. Separação do painel de plataforma

`POST /admin/v1/auth/login` e `/admin/v1/auth/refresh` vivem só no domínio
`manager.amparius.com`/control plane, com chave de assinatura própria
(`kid` disjunto do emissor de tenant) e tabela própria (`platform.*`). Um
token de tenant jamais passa em `auth.PlatformService.Authenticate` e
vice-versa — eles nem compartilham o par de chaves, então um JWT de tenant é
rejeitado na verificação de assinatura antes de qualquer checagem de claim.

**MFA de plataforma (decisão, não adiamento):** diferente do tenant,
`platform_admin` **mantém MFA obrigatório desde o início**, via TOTP
(RFC 6238) **auto-hospedado** — não é "MFA externo" (não depende de IdP
terceiro), então não conflita com o pedido de adiar integração externa. A
justificativa é o raio de impacto: `platform_admin` lê e escreve
cross-tenant (ADR 0040); um emissor sem MFA nesse papel é um ponto único de
falha para todos os tenants. Semente TOTP fica envelope-encriptada com chave
do cofre/KMS (nunca em texto puro no banco), idealmente delegada ao engine
TOTP do Vault corporativo quando disponível — ver Lacuna 1 para confirmação.

**Não é divergência, é obrigação já existente:** o TOTP obrigatório para
`platform_admin` não é uma decisão nova deste ADR — já é exigido pelo ADR
0040 (decisão 1) e pelo código vigente (`internal/platform/auth/platform.go`:
`PlatformService.Authenticate` rejeita `identity.MFA == false` e
`RequirePlatform` falha fechado). O adiamento de MFA pedido por Ronaldo vale
só para o login comum de tenant, clientes web/desktop e o painel SaaS do
cliente — nunca para `platform_admin`. Consequência prática para AUTH-01B:
**se o TOTP (emissão, verificação, matrícula de semente) não entrar junto com
o emissor próprio, `/admin/v1/auth/login` não é publicado** — continua
respondendo `503`, exatamente como hoje. Não há adaptador que force
`MFA == true` sem TOTP real nem relaxamento de `RequirePlatform` para
contornar a ausência de MFA nesta fatia.

### 10. Bloqueio por tentativas e rate-limit

`failed_attempts`/`locked_until` em `identity.app_user` (e equivalente em
`platform.admin_user`) são a fonte de verdade e **nunca falham aberto**: se o
Redis de rate-limit estiver indisponível, o login continua funcionando
(bloqueio por IP é defesa em profundidade, não obrigatória), mas o contador em
Postgres por usuário é sempre consultado e nunca contornado. Proposta de
parâmetros: 5 tentativas inválidas em 15 minutos bloqueiam a conta por 15
minutos; sucesso zera o contador. Rate-limit complementar por IP/e-mail em
Redis (sliding window) é defesa adicional contra força bruta distribuída, não
substitui o contador em banco.

### 11. Auditoria sem PII

`login_audit`/`admin_login_audit` gravam `user_id` (UUID opaco), nunca e-mail,
senha, token ou IP crú — consistente com o ADR 0024 (nada de subject/e-mail
em log) e o ADR 0030/contrato `docs/contracts/audit-lgpd-v1.md`. Tentativa com
e-mail **desconhecido** não gera linha em `login_audit` (tabela é RLS por
tenant e tenant é desconhecido nesse caso); ela só alimenta o rate-limit por
IP no Redis, para não abrir um caminho de enumeração de contas via auditoria.

### 12. Bootstrap do primeiro usuário (pending_auth → active)

Sem provedor de e-mail integrado ainda, o convite **não depende de envio de
e-mail nesta fatia**: `identity.create_onboarding` (ou um wrapper síncrono)
passa a devolver, na própria resposta HTTP de criação, um token de uso único
(`identity.credential_token`, `purpose='bootstrap'`, TTL curto — proposto 24h)
atrelado ao `owner_user_id`. Quem chamou o onboarding (o próprio formulário de
cadastro, no mesmo fluxo síncrono) usa esse token imediatamente para levar o
usuário à tela "defina sua senha" — `POST /v1/auth/bootstrap/complete` com
`{token, password}` define `password_hash`, muda `status` para `active` e já
devolve o primeiro par access/refresh (login automático). Convidar um colega
depois do onboarding (owner já autenticado) é o mesmo mecanismo via
`POST /v1/auth/invite`, mas a entrega do link ao convidado é manual
(copiar/colar) até existir envio de e-mail — ver Lacuna 2.

### 13. Recuperação de senha — desenho, entrega adiada

Mesma primitiva de token de uso único (`purpose='recovery'`).
`POST /v1/auth/recovery/request` com `{email}` sempre responde `202`
independente de o e-mail existir (evita enumeração) e gera o token quando o
usuário existe; `POST /v1/auth/recovery/complete` com `{token, password}` tem
semântica idêntica ao bootstrap. **O envio do token por e-mail está fora desta
fatia** — não há provedor de e-mail integrado — então `recovery/request`
nesta entrega inicial não tem canal de entrega: o contrato já existe para
quando um provedor for escolhido, mas o endpoint começa **desabilitado por
configuração** (responde `503 RECOVERY_UNAVAILABLE`) até AUTH-01B ligar um
canal de entrega. Isso evita publicar um fluxo de recuperação sem forma de o
usuário realmente receber o token.

### 14. Migração dos handlers `/_dev/*`

`internal/platform/http/server.go` já isola rotas dev-only por
`DevelopmentMode` (`cfg.Env == "development" && cfg.EnableDevOnboarding`, ver
`cmd/api/main.go:143`) — então nada hoje expõe `/_dev/*` em homologação/
produção por acidente; não é necessário um novo mecanismo de ocultação, só
migrar os handlers. Cada handler listado abaixo passa a ser registrado também
como `httpserver.AuthenticatedRoute` com a permissão já definida em
`auth.Permission` (ADR 0024), usando o principal/tenant/company que
`auth.Require` já injeta no contexto — os handlers de negócio não mudam de
assinatura, porque já esperam tenant/company via `context.Context`, não via
header dev:

`internal/modules/catalog/http.go`, `internal/modules/finance/http.go`,
`internal/modules/funeral/{contract,http,journey,schedule,service_order}_http.go`,
`internal/modules/party/{deceased,family_link,http,legal_entity}_http.go`.

`cmd/api/main.go` passa a construir `auth.Service`/`auth.PlatformService` com
o emissor próprio (via `OIDCVerifierFunc`/`PlatformVerifierFunc` chamando o
verificador JWT interno) em vez de deixar `Authenticator`/`PlatformAuthenticator`
como `nil`. As rotas `/_dev/*` continuam existindo só para o ciclo de
desenvolvimento local, sem tráfego de homologação/produção.

### 15. Cache offline de permissões no desktop

O cliente desktop Rust (AUTH-01D) reaproveita o **padrão** do ADR 0022 (envelope
assinado Ed25519, versão monotônica, TTL, fail-closed na expiração), mas com
um `key_id` próprio do principal de usuário — **não** a mesma chave do
snapshot de enforcement/entitlement do relay, que tem outro dono e outro ciclo
de rotação (ver Lacuna 6). O desktop faz login online (mesmos endpoints de
`/v1/auth`), recebe o access/refresh token normalmente e, adicionalmente, um
snapshot assinado de permissões efetivas (papéis + empresas permitidas) para
uso **enquanto offline**. Offline, o desktop decide autorização local pelo
snapshot em cache; expirado o TTL sem renovação, bloqueia (fail-closed) —
nunca continua liberado indefinidamente. Dinheiro/fiscal feito offline segue
as regras gerais de idempotência e conflito já fixadas em `AGENTS.md` §4/§8 e
não são reabertas aqui.

## Consequências e limites

- Nenhuma interface do ADR 0024/0040 muda; o custo de ter desenhado a costura
  provedor-agnóstica se paga aqui.
- `auth.Service`/`auth.PlatformService` precisam de um ajuste pontual em
  AUTH-01B: o gate hoje exige `MFAEnabled`/`identity.MFA == true`
  incondicionalmente. Como o tenant adia MFA, é necessário introduzir uma
  política explícita (`auth.MFAPolicy`: `Disabled|Optional|Required`) em vez
  de reescrever a regra; plataforma usa `Required`, tenant usa `Disabled`
  nesta fatia. Essa é uma mudança pequena e documentada, não uma
  reabertura do ADR 0024.
- Duas bases de credencial (`identity.*` na célula, `platform.*` no control
  plane) duplicam estrutura (hash, refresh, auditoria), mas preservam a
  separação do ADR 0040 — misturar as duas em uma tabela única com uma coluna
  "é plataforma?" reabriria a possibilidade de um bug vazar permissão de
  tenant para plataforma.
- Sem provedor de e-mail, bootstrap funciona (entrega síncrona na resposta do
  onboarding), mas convite pós-onboarding e recuperação de senha ficam com
  entrega manual ou desabilitada — ver Lacunas 2.
- Revogação "imediata" de access token não existe nesta fatia; o limite é o
  TTL de 10 minutos. Para alguns casos de uso (desligamento de funcionário com
  acesso a dinheiro) isso pode não ser aceitável — ver Lacuna 4.

## Alternativas descartadas

- **Adiar login inteiramente e manter `/_dev/*` em homologação**: descartado
  porque o pedido explícito de Ronaldo é ligar login real antes de expor o
  painel SaaS a terceiros; manter dev-only indefinidamente institucionaliza o
  bypass de autenticação.
- **Uma única tabela de credencial para tenant e plataforma**: descartado por
  violar a separação do ADR 0040 (plataforma não pode herdar nenhum mecanismo
  de tenant) e por multiplicar o raio de um bug de RLS.
- **JWT também para refresh token**: descartado porque um JWT de refresh de
  vida longa, se vazado, não pode ser revogado sem uma blocklist — o opaco
  com hash em banco permite revogação real (`revoked_at`) sem infraestrutura
  extra.
- **Adiar MFA também para `platform_admin`**: descartado por desenho (decisão
  3, §9) e por já ser obrigação do ADR 0040/código vigente, não uma escolha
  nova; o custo de implementar TOTP próprio é baixo comparado ao raio de
  impacto cross-tenant de uma conta de plataforma comprometida.
- **Sessão fixa em uma empresa (exigir novo login para trocar empresa)**:
  descartado porque o usuário com múltiplas empresas (ADR 0007 §6) trocaria de
  senha a cada troca de contexto; o refresh com `companyId` opcional resolve
  sem token adicional.

## Critérios de aceite

1. Login com e-mail/senha corretos emite access token (claims `sub`,
   `tenant_id`, `company_id`, `roles`, TTL 10 min, assinado Ed25519) e refresh
   token opaco; senha errada, conta bloqueada, conta `pending_auth` e empresa
   não vinculada ao usuário são recusados com código distinto e sem revelar
   qual condição falhou para e-mail inexistente vs. senha errada.
2. 5 tentativas inválidas em 15 minutos bloqueiam a conta; bloqueio é
   persistido em `identity.app_user` e sobrevive a reinício do processo;
   sucesso zera o contador.
3. `refresh` rotaciona o token (hash antigo marcado `rotated_at`, novo hash
   gravado); reusar um refresh já rotacionado revoga toda a família e é
   auditado como `refresh_reuse_detected`.
4. `refresh` com `companyId` diferente, mas vinculado ao usuário, emite access
   token da nova empresa sem novo login; `companyId` não vinculado é recusado.
5. `logout` revoga só a sessão atual; `revoke` revoga todas as sessões do
   usuário; token revogado falha em `refresh` subsequente.
6. `/admin/v1/auth/login` exige TOTP válido além de senha; token de tenant
   nunca é aceito por `auth.PlatformService` e vice-versa (chaves/`iss`
   disjuntos), provado por teste de contrato. Se o TOTP não estiver
   implementado, a rota permanece `503` — nunca é publicada sem MFA real (§9).
7. `identity.login_audit`/`admin_login_audit` nunca contêm e-mail, senha ou
   token em nenhuma linha, inclusive em falha — provado por teste que varre
   as colunas persistidas.
8. Bootstrap do primeiro usuário funciona fim a fim sem depender de e-mail
   (token devolvido na própria resposta de onboarding); `recovery/request`
   responde `503 RECOVERY_UNAVAILABLE` enquanto não houver canal de entrega
   configurado, nunca finge sucesso sem meio de entrega.
9. Todos os handlers listados em §14 respondem pela rota de produção
   autenticada com a permissão correta (401/403 nos casos negativos) e os
   testes que hoje provam `503 AUTHENTICATION_UNAVAILABLE` por
   `Authenticator == nil` passam a provar autenticação real.
10. `identity.resolve_login_candidates` nunca devolve `email`/`name`; com o
    mesmo e-mail em dois tenants, o login autentica no tenant correto pela
    senha sem expor ao cliente quantos tenants existiam; `amparius_app`
    continua sem `BYPASSRLS` — provado em PostgreSQL descartável (§5).
11. `git diff --check` limpo nesta entrega e nenhuma migration aplicada é
    editada (forward-only, AGENTS.md §2.8).

## Fatias (B/C/D)

### AUTH-01B — backend (emissor, credencial, sessão)

Entrega: migrations `identity.*`/`platform.*` (colunas e tabelas do §2),
função `identity.resolve_login_candidates` (§5), emissor JWT próprio
implementando `auth.OIDCVerifier`/`auth.PlatformVerifier`, `auth.MFAPolicy`
(§ Consequências), endpoints de `docs/contracts/auth-login-v1.md`, migração
dos handlers `/_dev/*` (§14), bootstrap sem e-mail (§12), `recovery/request`
como `503` controlado (§13). TOTP (emissão, verificação, matrícula de
semente) é entrega obrigatória desta fatia para `platform_admin` — sem ele,
`/admin/v1/auth/login` não é publicado (§9).

Critérios de aceite testáveis: itens 1–5, 8 (bootstrap), 9, 10 (resolução de
tenant) e 11 da lista acima; `go test ./internal/platform/auth
./internal/modules/identity ./cmd/api -count=1` cobrindo login válido/
inválido, bloqueio, rotação, reuso, troca de empresa e os 401/403/503 dos
handlers migrados.

### AUTH-01C — permissões (matriz papel×permissão + prova de RLS)

Entrega: matriz testada para `owner`, `admin`, `manager`, `operator`,
`auditor` e `platform_admin` contra cada `auth.Permission` existente;
prova de RLS em **PostgreSQL descartável** (testcontainers ou instância
efêmera local) confirmando que um usuário do tenant A nunca lê/escreve linha
do tenant B em `identity.app_user`, `identity.refresh_token`,
`identity.login_audit` e `identity.credential_token`, com o papel da
aplicação sem `SUPERUSER`/`BYPASSRLS` (mesmo padrão do teste de integração já
existente em `internal/modules/entitlement/integration_test.go`). A mesma
instância descartável prova o item 10 (§5): `identity.resolve_login_candidates`
não expõe PII e `amparius_app` não tem `BYPASSRLS`.

Critérios de aceite testáveis: itens 6 e 10; para cada papel × permissão, um
teste positivo e um negativo (ex.: `operator` nunca alcança
`entitlement:write`); para RLS, um teste de dois tenants simultâneos provando
0 linhas visíveis cruzadas, documentado com o comando reproduzível (DSN
descartável) no PR.

### AUTH-01D — clientes (telas web/painel e desktop)

Entrega: tela de login e estados (credencial inválida, conta bloqueada,
sessão expirada, troca de empresa) no painel SaaS (`/admin/v1`, com TOTP) e
no cliente web (`/v1`); no cliente desktop Rust, login online e cache offline
de permissões assinado com TTL e fail-closed (§15).

Critérios de aceite testáveis: cada estado de erro do contrato
(`docs/contracts/auth-login-v1.md`) tem uma tela/mensagem correspondente,
verificável por teste de interface ou snapshot; o desktop nega operação
offline após o TTL do snapshot expirar, com teste automatizado reproduzindo a
expiração (relógio controlado, sem depender de esperar o TTL real).

## Lacunas e decisões abertas (dependem de Ronaldo)

1. **MFA de plataforma via Vault TOTP engine vs. biblioteca própria.** Este
   ADR assume TOTP auto-hospedado obrigatório para `platform_admin` (§9,
   decisão). Falta confirmar se o cofre/KMS corporativo já tem um secrets
   engine de TOTP disponível (ex.: Vault `totp`) para delegar geração/
   verificação sem o Amparius guardar a semente, ou se AUTH-01B precisa
   implementar e envelope-encriptar a semente com uma chave do próprio cofre.
   **Default proposto (Analista), aguarda aprovação do Ronaldo:** semente
   TOTP própria envelope-encriptada no mesmo cofre já usado para os segredos
   de certificado fiscal; delegar a um secrets engine de Vault fica para
   depois, se/quando existir.
2. **Canal de entrega de convite/recuperação.** Sem provedor de e-mail
   integrado, convite pós-onboarding é um link copiável entregue manualmente
   e `recovery/request` fica `503` nesta fatia (§12/§13). Confirmar se isso é
   aceitável até o primeiro piloto ou se e-mail transacional (e qual
   provedor) é pré-requisito antes de clientes reais. **Default proposto
   (Analista), aguarda aprovação do Ronaldo:** convite/recuperação manual
   (link copiável) aceito no piloto; `recovery/request` permanece `503` até
   existir canal de entrega real.
3. **TTLs e limites propostos.** Access token 10 min, refresh 30 dias,
   bloqueio em 5 tentativas/15 min (§4/§10) são defaults propostos, não
   testados com o uso real de operador de loja (ex.: caixa o dia todo sem
   reautenticar). Confirmar se esses valores servem ou precisam de ajuste
   antes de ir para produção. **Default proposto (Analista), aguarda
   aprovação do Ronaldo:** manter os valores como estão; revisar com
   telemetria real de operador depois do piloto, não antes.
4. **Revogação imediata de access token.** Hoje a defesa é o TTL curto; não
   há blocklist (§7). Confirmar se um caso como desligamento de funcionário
   com acesso a caixa/fiscal exige revogação efetiva em segundos (o que
   adiciona Redis como dependência obrigatória, não só de rate-limit) ou se
   10 minutos de tolerância é aceitável no MVP. **Default proposto
   (Analista), aguarda aprovação do Ronaldo:** não exigir Redis obrigatório
   agora; aceitar até 10 minutos de tolerância e registrar como risco
   conhecido e aceito no piloto.
5. **Guarda da chave de assinatura Ed25519.** Confirmar o mecanismo real de
   cofre/KMS disponível hoje (nome do serviço, como o processo `cmd/api`
   recupera o material de assinatura em runtime) — o ADR assume uma
   referência opaca (`key_id`), mas AUTH-01B precisa de um alvo concreto para
   implementar. **Default proposto (Analista), aguarda aprovação do
   Ronaldo:** mesmo cofre já usado para os segredos de certificado fiscal; se
   não existir um cofre central hoje, isso é pré-requisito de infraestrutura
   antes de AUTH-01B, não uma decisão deste ADR.
6. **Chave do snapshot offline do desktop.** Confirmar se o desktop reusa a
   infraestrutura de chave do relay (ADR 0022, hoje pensada para o pacote de
   enforcement/entitlement) ou se recebe um `key_id` próprio do principal de
   usuário (proposta deste ADR, §15) — afeta quem no control plane assina o
   quê e com qual rotação. **Default proposto (Analista), aguarda aprovação
   do Ronaldo:** `key_id` próprio do principal de usuário, nunca a chave do
   relay.
7. **Resolução de tenant no login (lacuna bloqueante do parecer do
   Analista).** O mecanismo concreto está desenhado em §5:
   `identity.resolve_login_candidates`, função `SECURITY DEFINER` mínima e
   auditada, sem `BYPASSRLS` em `amparius_app`. Falta confirmar se o campo
   opcional `tenantHint` no contrato é suficiente para os fluxos de
   convite/onboarding com tenant fixo, ou se o piloto precisa de um
   seletor de tenant explícito na UI desde já. **Default proposto
   (Analista), aguarda aprovação do Ronaldo:** seguir o mecanismo de §5 como
   está (função dedicada, nunca papel geral com `BYPASSRLS`); `tenantHint`
   só entra em uso quando um fluxo concreto (convite) precisar.

## Verificação

```text
go test ./internal/platform/auth ./internal/modules/identity ./cmd/api -count=1
go test -p 1 ./... -count=1
```

Nenhum teste automatizado é entregue nesta fatia (ADR + contrato apenas); os
comandos acima são os que AUTH-01B deve manter verdes ao final, reproduzindo
o padrão já usado pelo ADR 0024/0038.
