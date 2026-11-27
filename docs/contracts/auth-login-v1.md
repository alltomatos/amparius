# Contrato HTTP de autenticação v1 (`auth-login-http-v1`)

Status: desenhado em 27/11/2026 (AUTH-01A, issue #103) · a implementar em
AUTH-01B · decisões em `docs/adr/0048-auth-emissor-proprio-login.md`.

Dois conjuntos de rotas, completamente separados (chave, tabela, emissor e
`iss`/`aud`):

- **Tenant** — base `/v1/auth`, servido em `app.amparius.com`.
- **Plataforma** — base `/admin/v1/auth`, servido em `manager.amparius.com`
  (ADR 0040). Exige TOTP em todo login.

Envelope de erro comum a ambos:

```json
{"code":"STRING","message":"texto curto","details":{},"traceId":"uuid-ou-vazio"}
```

Nenhuma resposta, inclusive de erro, contém e-mail, senha, token ou claim não
essencial. `Cache-Control: no-store` em toda resposta de `/v1/auth/*` e
`/admin/v1/auth/*`.

## Tenant — `/v1/auth`

### `POST /v1/auth/login`

Corpo:

```json
{"email":"dono@exemplo.com","password":"...","companyId":"uuid","tenantHint":"slug-opcional"}
```

`companyId` é obrigatório: não existe sessão "sem empresa". `tenantHint` é
**opcional** e usado só por fluxos que já sabem o tenant (ex.: link de
convite/onboarding) — a maioria das chamadas não envia. Sem `tenantHint`, o
servidor resolve o tenant pelo e-mail (ver "Resolução de tenant" abaixo antes
da tabela de erros). Resposta `200`:

```json
{
  "accessToken": "eyJ...",
  "accessTokenExpiresIn": 600,
  "refreshToken": "opaco-base64url",
  "refreshTokenExpiresIn": 2592000,
  "tokenType": "Bearer",
  "user": {"id": "uuid", "roles": ["owner"], "companyId": "uuid"}
}
```

### Resolução de tenant (sem `tenantHint`)

`app.amparius.com` é domínio único: o corpo não traz `tenantId`, então o
servidor precisa descobrir o tenant pelo e-mail antes de poder usar o
caminho normal protegido por RLS. Mecanismo (desenho completo em
`docs/adr/0048-auth-emissor-proprio-login.md` §5): uma função
`SECURITY DEFINER` mínima e auditada,
`identity.resolve_login_candidates(email)`, devolve só
`tenant_id, user_id, password_hash, password_algo, status, failed_attempts,
locked_until` para o e-mail informado — nunca `email`/`name`, e sem conceder
`BYPASSRLS` a `amparius_app`.

- 0 candidatos: verificação `argon2id` contra um hash de isca (nunca real)
  para igualar o custo de CPU, depois `401 INVALID_CREDENTIALS`.
- 1 candidato: fluxo normal — senha correta define o tenant da sessão.
- N candidatos (mesmo e-mail em tenants diferentes): senha verificada contra
  cada candidato até achar exatamente um que bata; nenhum detalhe sobre
  quantos tenants existem é exposto ao cliente em nenhum dos casos.

Rate-limit por `IP + e-mail` normalizado (Redis) cobre o caso de e-mail
inexistente, que não tem `failed_attempts` em banco.

Erros:

| HTTP | code | Quando |
|---|---|---|
| 401 | `INVALID_CREDENTIALS` | e-mail inexistente, senha incorreta ou resolução de tenant ambígua sem candidato único (mesma resposta em todos os casos — nunca revelar qual) |
| 403 | `ACCOUNT_LOCKED` | `locked_until` no futuro (só depois de um tenant já resolvido) |
| 403 | `ACCOUNT_PENDING_AUTH` | usuário existe mas não concluiu bootstrap (ADR 0048 §12) |
| 403 | `COMPANY_NOT_ALLOWED` | `companyId` não vinculado ao usuário (`identity.user_company`) |
| 400 | `VALIDATION_ERROR` | corpo malformado/campo ausente |
| 503 | `AUTHENTICATION_UNAVAILABLE` | emissor/cofre de chave indisponível |

Toda tentativa com exatamente um tenant resolvido grava
`identity.login_audit` com `user_id` e `result`; tentativa com e-mail
desconhecido ou resolução ambígua sem candidato único não grava (não há um
único tenant a escopar) e só alimenta o rate-limit por IP+e-mail.

### `POST /v1/auth/refresh`

Corpo:

```json
{"refreshToken":"opaco-base64url","companyId":"uuid-opcional"}
```

Sem `companyId`, mantém a empresa do token anterior. Com `companyId`
diferente, valida contra `identity.user_company` antes de emitir. Resposta
`200` igual ao login (novo `accessToken` + novo `refreshToken` — rotação
sempre acontece, mesmo sem troca de empresa).

Erros:

| HTTP | code | Quando |
|---|---|---|
| 401 | `REFRESH_INVALID` | token inexistente, expirado ou já revogado |
| 401 | `REFRESH_REUSE_DETECTED` | token já havia sido rotacionado (família toda revogada nesta resposta) |
| 403 | `COMPANY_NOT_ALLOWED` | `companyId` informado não vinculado ao usuário |

Cliente pode enviar `Idempotency-Key` opcional (1–200 caracteres): uma
repetição com a mesma chave e o mesmo `refreshToken` dentro de uma janela
curta (proposto 10s) devolve o mesmo par emitido, sem rotacionar de novo —
protege um retry de rede de disparar falso `REFRESH_REUSE_DETECTED` contra o
próprio cliente.

### `POST /v1/auth/logout`

Requer `Authorization: Bearer <accessToken>` válido. Corpo:
`{"refreshToken":"opaco-base64url"}`. Revoga só essa sessão (família marcada
`revoked_at`, `revoked_reason='logout'`). Resposta `204`. Token já revogado
responde `204` também (logout é idempotente por natureza).

### `POST /v1/auth/revoke`

Requer `Authorization: Bearer <accessToken>` válido e senha atual no corpo
(`{"password":"..."}`) — reautenticação explícita antes de uma ação
destrutiva para a própria sessão. Revoga **todas** as famílias de refresh
token do usuário (todas as sessões, todos os dispositivos). Resposta `204`.
Uso administrativo equivalente (ex.: suspeita de conta comprometida) é
exposto só via `/admin/v1` por um operador com `platform:write`, nunca pelo
próprio tenant sem senha.

### `POST /v1/auth/bootstrap/complete`

Corpo: `{"token":"...","password":"..."}`. `token` é o valor cru devolvido
uma única vez na resposta de onboarding ou de convite (ADR 0048 §12) — o
servidor só guarda o hash. Define `password_hash`, muda `status` de
`pending_auth` para `active`, consome o token (`consumed_at`) e devolve o
mesmo corpo de `/v1/auth/login` (login automático), exigindo `companyId`
também aqui (o usuário recém-ativado escolhe a empresa, mesmo tendo só uma
vinculada no onboarding inicial).

Erros: `410 TOKEN_EXPIRED`, `409 TOKEN_ALREADY_USED`,
`400 VALIDATION_ERROR` (senha fora da política mínima).

### `POST /v1/auth/invite`

Autenticado, exige `identity:write` e `Idempotency-Key`. Corpo:
`{"email":"...","name":"...","roleCodes":["manager"],"companyIds":["uuid"]}`.
Cria o `identity.app_user` em `pending_auth` com os papéis e empresas dados e
devolve o token de bootstrap cru **uma única vez** na resposta
(`{"bootstrapToken":"..."}`) para o chamador copiar/enviar manualmente — não
há envio de e-mail nesta fatia (ADR 0048 §12, Lacuna 2).

### `POST /v1/auth/recovery/request`

Corpo: `{"email":"..."}`. Enquanto não houver canal de entrega configurado,
responde sempre `503 RECOVERY_UNAVAILABLE` (nunca `202` fingindo que algo foi
enviado). Quando um provedor de e-mail for integrado (fora desta fatia), a
resposta vira `202` incondicional (mesmo para e-mail inexistente, para não
permitir enumeração), com o token gerado e entregue de forma assíncrona.

### `POST /v1/auth/recovery/complete`

Mesma semântica de `bootstrap/complete`, mas com `purpose='recovery'` e sem
mudar `status` (o usuário já era `active`). Mesmos erros de token.

## Plataforma — `/admin/v1/auth`

### `POST /admin/v1/auth/login`

Corpo: `{"email":"...","password":"...","totpCode":"123456"}`. `totpCode` é
obrigatório em toda chamada — não existe "primeiro login sem MFA" para
plataforma (ADR 0048 §9, obrigação já existente desde o ADR 0040, não uma
decisão nova). **Esta rota não é publicada enquanto o TOTP não estiver
implementado**: até lá, continua respondendo `503
AUTHENTICATION_UNAVAILABLE`, como hoje — nunca um adaptador que force
`MFA == true` sem verificação real. Resposta `200`:

```json
{
  "accessToken": "eyJ...",
  "accessTokenExpiresIn": 600,
  "refreshToken": "opaco-base64url",
  "refreshTokenExpiresIn": 2592000,
  "tokenType": "Bearer",
  "user": {"id": "uuid", "roles": ["platform_admin"]}
}
```

Erros equivalentes ao tenant, mais `401 TOTP_INVALID` e
`403 TOTP_NOT_ENROLLED` (conta de plataforma criada sem TOTP configurado —
plataforma não tem "ativação pendente sem MFA").

### `POST /admin/v1/auth/refresh` e `POST /admin/v1/auth/logout`

Mesma semântica do tenant, sem `companyId` (plataforma não tem empresa) e
sobre `platform.admin_refresh_token`/`platform.admin_login_audit` no control
plane, nunca na célula.

Não há `/admin/v1/auth/bootstrap` nesta fatia: a primeira conta
`platform_admin` é provisionada fora de banda (operação administrativa
direta no control plane, como já ocorre hoje para qualquer configuração de
plataforma) — abrir um endpoint de auto-cadastro de `platform_admin` exigiria
outra decisão de produto, fora do escopo desta fatia.

## Claims do access token (referência)

**Tenant:**

```json
{"iss":"amparius-saas","sub":"uuid-user","tenant_id":"uuid","company_id":"uuid","roles":["owner"],"iat":0,"exp":0,"jti":"uuid"}
```

**Plataforma:**

```json
{"iss":"amparius-platform","aud":"manager","sub":"uuid-admin","roles":["platform_admin"],"iat":0,"exp":0,"jti":"uuid"}
```

Assinatura `EdDSA` (Ed25519); header carrega `kid`. Verificador aceita a
chave pública do `kid` ativo e do anterior durante a janela de rotação, nunca
uma chave fora dessa lista.

## Rate-limit e bloqueio

Contador autoritativo é `failed_attempts`/`locked_until` em
`identity.app_user`/`platform.admin_user` — nunca falha aberto. Rate-limit por
IP (Redis, sliding window) é defesa adicional e pode falhar aberto sem
bloquear login legítimo quando o Redis está fora do ar.

## Testes de contrato mínimos (AUTH-01B)

- Resolução de tenant (PostgreSQL descartável, ADR 0048 §5): mesmo e-mail em
  dois tenants, `identity.resolve_login_candidates` devolve exatamente 2
  linhas sem `email`/`name`; `amparius_app` sem `BYPASSRLS`; login com senha
  certa autentica no tenant certo sem expor quantos tenants existiam.
- Login válido/inválido, bloqueio após N tentativas, zeragem no sucesso.
- Refresh: rotação simples, troca de empresa válida/inválida, reuso detectado
  revogando a família, replay com `Idempotency-Key` idêntico não duplica
  efeito.
- Logout idempotente; revoke exige senha e invalida todas as sessões.
- Bootstrap/convite/recovery: token de uso único, expiração, reuso recusado.
- Plataforma: login sem TOTP recusado; token de tenant rejeitado no
  verificador de plataforma e vice-versa (chaves/`iss` disjuntos).
- Nenhuma linha de auditoria com e-mail, senha ou token em nenhum caminho,
  inclusive de erro — teste que varre colunas persistidas.
