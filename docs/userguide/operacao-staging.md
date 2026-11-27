# Operação de staging

Este guia cobre a verificação de uma célula de staging com dados sintéticos.
Não use os comandos de teste contra produção.

## Saúde da API

- `/healthz` responde se o processo está vivo.
- `/readyz` responde `200` somente quando PostgreSQL, Redis, RabbitMQ e o
  object storage S3-compatible respondem; responde `503` se algum probe falhar.
- Se `AMPA_CONTROL_POSTGRES_DSN` estiver configurado, o banco do control plane
  também é obrigatório.

O object storage é configurado por `OBJECT_STORAGE_ENDPOINT`,
`OBJECT_STORAGE_REGION`, `OBJECT_STORAGE_BUCKET`,
`OBJECT_STORAGE_ACCESS_KEY` e `OBJECT_STORAGE_SECRET_KEY`. Para Cloudflare R2,
use a URL da conta, região `auto` e `OBJECT_STORAGE_USE_PATH_STYLE=false`.
Nenhuma chave deve ser colocada no repositório.

O Compose local não inclui servidor de object storage. Para desenvolvimento sem um bucket
remoto, use qualquer endpoint S3-compatible local e configure
`OBJECT_STORAGE_USE_PATH_STYLE=true`; sem essa configuração, a API ainda sobe,
mas permanece `not_ready` de forma explícita.

O corpo de readiness informa apenas `status`, `latency_ms` e o estado de cada
check. DSNs, senhas e erros crus nunca aparecem na resposta.

## Backup e restore de teste

1. Configure `AMPA_BACKUP_TEST_ADMIN_DSN` para uma conexão administrativa de
   um ambiente de teste.
2. Execute:

   ```powershell
   .\scripts\backup-restore.ps1 test
   ```

3. Confirme a mensagem `BACKUP/RESTORE TEST PASS`.

O comando cria e remove um banco temporário e usa somente o marcador
`synthetic-only`. Ausência de `pg_dump`, `pg_restore` ou `psql` é erro; não há
fallback que simule o restore.

Para gerar um backup autorizado:

```powershell
$env:AMPA_BACKUP_DSN = 'postgres://<admin>:<secret>@host:5432/amparius?sslmode=disable'
$env:AMPA_BACKUP_FILE = 'D:\backups\amparius.dump'
.\scripts\backup-restore.ps1 backup
```

Para restaurar em um banco existente, o operador precisa usar
`-AllowDestructiveRestore` conscientemente. O backup deve ser preservado e
verificado antes da operação.

## Rollback da API

Antes do rollback, gere um backup e execute o modo `plan`:

```powershell
.\scripts\rollback-staging.ps1 plan -PreviousImage amparius-api:previous -BackupFile D:\backups\staging.dump
```

Depois, com o backup existente e a imagem anterior disponível:

```powershell
.\scripts\rollback-staging.ps1 apply -PreviousImage amparius-api:previous -BackupFile D:\backups\staging.dump -AllowRollback
```

O rollback troca somente a imagem da API. Ele não executa `goose down`, não
apaga volume e não desfaz migration aplicada. Após o serviço subir, verifique
`/healthz` e `/readyz` uma vez e registre o resultado. Se o schema não for
compatível, interrompa o tráfego e siga o procedimento de restore aprovado.

## Evidência e limites

O p95 reproduzível é executado por:

```powershell
.\scripts\benchmark.ps1 -Iterations 1000
```

Essa medição usa uma dependência sintética e representa somente o caminho HTTP
do probe. O smoke com Compose valida a topologia local; o object storage é
validado pelo teste S3 sintético ou por R2 autorizado. O teste de
backup não prova PITR/WAL contínuo, criptografia do repositório, restore de
object storage ou recuperação de produção.

O probe S3 sintético pode ser repetido sem credenciais:

```powershell
go test ./internal/platform/readiness -run 'TestS3ChecksHeadBucketAgainstGenericEndpoint|TestS3UnconfiguredFailsClosed' -count=1 -v
```

## Homologação na Omniroute

A API do Compose escuta em `127.0.0.1:18080`. A homologação pública usa
`https://hml.amparius.com` e `https://hml.amparius.com.br`, com certificados
Let's Encrypt. O Caddy escuta em TCP 443 e usa ACME TLS-ALPN-01. A porta 80
continua com o nginx dos demos, portanto use URLs com `https://`.

O [Caddyfile](../../deploy/staging/Caddyfile) encaminha somente `GET` e
`HEAD` de `/healthz` e `/readyz` para a API. O edge serve duas superfícies públicas com dados sintéticos:

- [Cliente web](https://hml.amparius.com/) — navegar pelos destinos da loja,
  consultar e cadastrar exemplos, selecionar responsável e serviço, enfileirar
  atendimento, simular reconexão e reenviar a mesma operação.
- [Painel SaaS](https://manager.amparius.com/) — abrir a empresa de exemplo,
  selecionar **Funerária**, ajustar quota, conceder ou revogar licença e
  conferir a versão e auditoria de demonstração.

O painel SaaS é servido na raiz de `manager.amparius.com` (mesmo conjunto
sintético de `/`, `/styles.css`, `/helpers.js`, `/app.js`, `/panel-demo.js`,
`/staging.css` e `/assets/brand/amparius-logo.svg`). `manager.amparius.com` é
a superfície administrativa definida no ADR 0040. As duas superfícies são
separadas: o cliente web não tem link para o painel e `hml.amparius.com` não
serve `/painel/`.

A fila e as mutações são isoladas por aba e reiniciam ao recarregar. A
política de conteúdo impede conexões da aplicação com API ou relay. A
versão `.com.br` continua com os probes públicos. Para testar rotas de
desenvolvimento com banco, abra o túnel a partir de uma estação autorizada:

```sh
ssh -N -L 127.0.0.1:18080:127.0.0.1:18080 omniroute
```

<a id="cadastro-pj-web-privado"></a>

### Cadastro operacional de pessoa jurídica no cliente web privado

Em **Cadastros → Pessoa jurídica**, informe **Razão social** e **CNPJ**
numérico ou alfanumérico. **Nome fantasia** é opcional. Use somente dados
sintéticos em homologação. Exemplo de teste: Funerária Exemplo,
`12.ABC.345/01DE-35`.

Depois de recarregar a página, digite a razão social, o nome fantasia ou o
CNPJ em **Buscar pessoa jurídica** para recuperar o cadastro persistido.
A lista mostra até 25 resultados por consulta. Ao apagar o termo, a lista
é limpa. Selecione a empresa para ver os detalhes. Se a busca não encontrar
a empresa, confira o termo. Para um CNPJ legado mascarado, busque pelo nome.

- **Adicionar contato**. Escolha E-mail, Telefone, WhatsApp ou Outro e
  informe o valor. **Principal** substitui o principal do mesmo tipo.
- **Adicionar endereço**. Preencha logradouro, número, cidade, UF e CEP.
  **Principal** substitui o endereço principal anterior.
- **Vincular representante**. Selecione uma PF já cadastrada no grupo
  e um papel: Representante legal, Administrador, Procurador ou Contato.
  A mesma PF com o mesmo papel só pode ser vinculada uma vez à mesma PJ.

As ações atualizam o detalhe da PJ selecionada após a confirmação. Se
houver falha de conexão, restabeleça a conexão e consulte o cadastro antes
de repetir. Em caso de CNPJ duplicado, localize a PJ existente. Se a política
estiver indisponível, acione o responsável pela privacidade. Contato, endereço
e representação dependem da política de retenção aprovada pelo DPO e são
gravados com auditoria na mesma transação.

A operação depende de sessão vinculada à empresa e papel autorizado no
fluxo v1. Em ambiente privado de desenvolvimento, o modo opt-in atende
apenas a dados sintéticos. A prévia pública é uma demonstração sintética.
Consulte o [contrato v1](../contracts/party-legal-entity-v1.md).

<a id="loja-01-04-leituras-autenticadas"></a>

### Loja 01/04 — verificar agenda e títulos pela API

Use dados sintéticos em ambiente privado de teste. Envie
`Authorization: Bearer <token>` nas duas consultas. A API valida o vínculo
do usuário com a empresa e o grupo econômico (tenant) da credencial.
A integração injeta um autenticador de teste. O bootstrap ainda depende de
OIDC/MFA e, sem autenticador, retorna `503 AUTHENTICATION_UNAVAILABLE`.
A prévia pública usa dados sintéticos e funciona sem conexão com a API.

1. **Loja 01 — Agenda.** Com `funeral:read` e licença Funerária vigente na
   empresa, consulte `GET /v1/funeral/agenda?date=2026-11-26&time_zone=America/Fortaleza`.
   Informe a data civil e o fuso IANA da loja. A janela padrão é um dia civil.
   Para até 31 dias, informe também `end_date`, com limite exclusivo.
   Confira os agendamentos da empresa em `items`. Cada item cujo estado
   persistido é `scheduled` aparece em `pending` como confirmação pendente,
   identificada por `schedule_id`. Em um dia sem registros, ambas as listas
   vêm vazias. Licença ausente, revogada ou expirada retorna
   `403 ENTITLEMENT_MODULE_NOT_LICENSED`. Fonte de licenças indisponível
   retorna `503 ENTITLEMENT_UNAVAILABLE`.
   [Contrato da agenda](../contracts/funeral-schedule-http-v1.md#agenda-da-empresa).
2. **Loja 04 — Títulos.** Com `finance:read`, consulte
   `GET /v1/finance/titles?direction=receivable&status=open&due_to=2026-11-30&limit=50`.
   A consulta abrange todo o grupo econômico, inclusive outras empresas
   do mesmo tenant. Use `direction` e `status` para filtrar pela direção e
   pelo estado persistido. `due_from` e `due_to` são limites inclusivos de
   vencimento. Para continuar em ordem de vencimento e ID, mantenha os
   filtros e envie `next_cursor` como `cursor`. A última página omite
   `next_cursor`. Sem resultados, `items` vem vazio.
   [Contrato financeiro](../contracts/finance-titles-payments-v1.md).

No cliente web privado, inclua a sessão Bearer, a data civil e o fuso IANA
no [bootstrap da loja](../../web/staging/README.md#configurar-o-modo-operacional).
Abra **Painel do dia** para ver a agenda e as confirmações pendentes da
empresa. Os horários aparecem no fuso indicado e **Atualizar agenda**
consulta novamente o mesmo dia. A fila de **Sincronização** é uma simulação
local. Abra **Financeiro**, aplique os filtros de situação, direção e
vencimento inclusivo e use **Próxima página** para continuar com os mesmos
filtros. A última página oculta o botão. A lista mostra os valores
nominais dos títulos do grupo econômico.

As duas telas apresentam a indisponibilidade da leitura quando o modo
privado não tem sessão Bearer. A #79 segue aberta para parcelas,
recebimentos, caixa, conciliação e validação com autenticação real. Consulte a [evidência das leituras](../design/mvp-store-dashboard-read-evidence.md).

### Roteiro da prévia visual

- No cliente web, abra **Cadastros**, selecione um tipo e clique em
  **Cadastrar exemplo**. Cadastre uma pessoa física, um falecido e um serviço.
  Filtre por nome e vincule os dois cadastros em **Vínculos familiares**.
- Abra **Atendimento**, escolha o responsável e o serviço cadastrados e
  confirme duas vezes. **Sincronização** mostra uma operação com os exemplos
  escolhidos. Simule a reconexão e a sincronização. Repetir o envio preserva a
  mesma sequência. Recarregar repõe os exemplos iniciais.
- Os menus **Contratos**, **Cemitério**, **Flores**, **Financeiro**, **Fiscal**
  e **Relatórios** informam o que ainda está indisponível. A
  [matriz do MVP](../design/mvp-mockup-gap-matrix.md) aponta os contratos e a
  ordem das próximas integrações.
- Em **Painel SaaS**, selecione a empresa e o módulo **Funerária**. A revogação
  altera o indicador de licenças ativas e aparece em **Auditoria**. Recarregar a
  página repõe o cenário inicial.
- A logo oficial é servida pelo caminho exato
  `/assets/brand/amparius-logo.svg`. A política de conteúdo permite essa imagem
  local e mantém bloqueadas as conexões da aplicação. As galerias de referência
  estão no [catálogo de mockups](../design/MOCKUPS.md).

### Instalar o edge

Confirme que os dois registros DNS apontam para a VPS e que TCP 443 está
disponível. Na raiz do checkout da VPS, execute:

```sh
image=caddy@sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0
release=/etc/amparius-staging/preview-$(git rev-parse --short HEAD)
sh deploy/staging/prepare-preview.sh "$release"
install -D -m 644 deploy/staging/Caddyfile /etc/amparius-staging/Caddyfile
docker run --rm --network none \
  -v /etc/amparius-staging/Caddyfile:/etc/caddy/Caddyfile:ro \
  "$image" caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
ufw allow 443/tcp comment 'amparius-staging-https'
docker run -d --name amparius-staging-https --restart unless-stopped --network host \
  -v /etc/amparius-staging/Caddyfile:/etc/caddy/Caddyfile:ro \
  -v "$release:/srv/amparius-preview:ro" \
  -v amparius-staging-caddy-data:/data \
  -v amparius-staging-caddy-config:/config "$image"
```

Se o edge já estiver instalado, salve a configuração e o caminho da versão
anterior. Valide a nova configuração e os arquivos antes de substituir o
container do edge, preservando o estado ACME. Para voltar, recrie somente
o edge com a configuração e o diretório anteriores.

Verifique de uma rede externa, com validação normal de certificado TLS:

```sh
for host in hml.amparius.com hml.amparius.com.br; do
  curl -fsS "https://$host/healthz"
  curl -fsS "https://$host/readyz"
  curl -sS -o /dev/null -w '%{http_code}\n' "https://$host/_dev/"
  curl -sS -o /dev/null -w '%{http_code}\n' "https://$host/v1/context"
done
curl -fsS https://hml.amparius.com/ -o /dev/null
curl -fsS https://manager.amparius.com/ -o /dev/null
```

O resultado esperado é `200` nos dois probes e nas duas superfícies, `404` em
`/_dev/`, `/v1/context` e `hml.amparius.com/painel/`. No host,
`docker port staging-api-1 8080/tcp` deve mostrar `127.0.0.1:18080`. Para
retirar o edge, pare apenas `amparius-staging-https` e remova a regra UFW
criada para ele se a porta 443 continuar reservada exclusivamente a esse
serviço. Preserve os volumes do Caddy para reutilizar o estado do ACME.

### Evidência da atualização de 26/11/2026

- A API de `6947bf1` passou em `/healthz` e `/readyz` nos dois hostnames.
  PostgreSQL, Redis, RabbitMQ e R2 responderam no readiness.
- Os dois backups PostgreSQL passaram na verificação SHA-256 e foram
  restaurados em uma instância isolada. As migrações ensaiadas e aplicadas
  atualizaram a célula de `0018` para `0035` e o control plane de `0002`
  para `0003`. O papel `amparius_app` tinha RLS ativo nas tabelas consultadas
  e atributos `NOSUPERUSER` e `NOBYPASSRLS`.
- Para validar a prévia localmente, execute
  `node --test web/control-plane/tests/*.test.mjs`. Para a estação Rust,
  siga o [guia do cliente](../../client/README.md) com identificadores
  sintéticos e rode `cargo test --locked --manifest-path client/Cargo.toml`
  e `cargo test --locked --manifest-path relay/Cargo.toml`.
- Na validação da prévia, passaram 10 testes web, 52 do cliente e 51 do relay.
  Em instância descartável de PostgreSQL 16, passaram a jornada funeral
  (OS, agenda, replay, rollback e isolamento) e os testes CP-06 de licença,
  cota, auditoria, versão e isolamento. Os papéis dos testes não tinham
  `SUPERUSER` nem `BYPASSRLS`. Os grants necessários para alcançar os
  triggers append-only foram aplicados somente no banco descartável de QA.
  O edge entregou os dois caminhos da prévia e manteve `404` nas rotas de
  desenvolvimento e de contexto autenticado.
- O login administrativo para operações reais depende de OIDC/MFA
  (ADR 0024). A jornada real entre estação, relay e SaaS e a emissão
  autorizada de NFS-e seguem nas issues #25, #59 e #60.

### Evidência da publicação de 26/11/2026 (`f4e8fac`)

- O merge `f4e8fac5cd1d3b6a8c6d74b56b81a2343be613a8` está em `main` e a
  prévia pública passou a servir esse pacote, substituindo `preview-ad4d78f`.
- O pacote foi gerado por `deploy/staging/prepare-preview.sh` a partir desse
  commit (9 arquivos) e conferido byte a byte contra uma reconstrução limpa.
  O [Caddyfile](../../deploy/staging/Caddyfile) publicado tem SHA-256
  `1d9251b1eb00801c812f00800a9fe77374abd079d2281232042c35d6345852ba`.
- `caddy validate` com a imagem pinned retornou `Valid configuration`.
- O container `amparius-staging-https` foi recriado com os mounts
  `/etc/amparius-staging/preview-f4e8fac` e
  `/etc/amparius-staging/Caddyfile.f4e8fac`, preservando os volumes
  `amparius-staging-caddy-data` e `amparius-staging-caddy-config` (estado
  ACME). O container anterior ficou parado como
  `amparius-staging-https-ad4d78f` para rollback.
- Após publicar, `hml.amparius.com` e `hml.amparius.com.br` responderam
  `200` em `/healthz` e `/readyz`, e `404` em `/_dev/` e `/v1/context`.
- A prévia em `hml.amparius.com` entregou `200` em `/`, `/staging.css`,
  `/client-demo.js`, `/painel/`, `/painel/styles.css`, `/painel/helpers.js`,
  `/painel/app.js` e `/painel/panel-demo.js`.
- `/assets/brand/amparius-logo.svg` respondeu `200` com `image/svg+xml` e
  SHA-256 `a63fb69953774ce728eabca70864ce0378c5b5c445ade26a3c0b879f0f72ca99`,
  igual ao arquivo empacotado.
- Os cabeçalhos `cache-control: no-store`, a CSP restritiva com
  `connect-src 'none'` e `x-robots-tag: noindex, nofollow` continuam
  aplicados.
- Para voltar, pare o container atual e recrie o edge com
  `preview-ad4d78f` e `Caddyfile.ad4d78f`; o container parado
  `amparius-staging-https-ad4d78f` mantém a configuração anterior.

### Evidência da publicação em `manager.amparius.com`

- Pacote `preview-395c8ef` gerado por `deploy/staging/prepare-preview.sh`
  (16 arquivos), incluindo `manager/` com o painel na raiz.
- `caddy validate` com a imagem pinned retornou `Valid configuration`.
- O Caddy emitiu certificado Let's Encrypt para `manager.amparius.com`
  (TLS-ALPN-01), válido até 24/02/2027.
- `manager.amparius.com`: `/`, `/styles.css`, `/helpers.js`, `/app.js`,
  `/panel-demo.js`, `/staging.css` e `/assets/brand/amparius-logo.svg` em
  `200`; `/healthz` e `/readyz` em `200`; `/v1/context` e `/painel/` em `404`.
- `hml.amparius.com` (`/`, `/painel/`, probes) e `hml.amparius.com.br`
  (probes) continuaram em `200` — sem regressão.
- O HTML servido em `/` tem o mesmo SHA-256 de
  `preview-395c8ef/manager/index.html`.
- O container anterior ficou parado como
  `amparius-staging-https-pre-395c8ef` para rollback.
### 2026-11-26 · Navegação e cadastros de exemplo

- O cliente web passou a servir o pacote 0f6a057
  ([PR #70](https://github.com/alltomatos/amparius/pull/70)). SHA-256 de
  index.html: 5ce26eaabc2dcec8902128b25ccef703fcd2bd1456fd7c41688b7523d767bc21.
  HTML, JavaScript e CSS públicos coincidiram com o pacote e mantiveram a
  política connect-src 'none'.
- O navegador abriu Cadastros no domínio público e cadastrou uma PF de
  exemplo. Os probes responderam 200 em hml.amparius.com e
  hml.amparius.com.br. Em hml.amparius.com, /_dev/parties, /v1/context e
  /painel/ responderam 404.
- O diretório manager foi preservado byte a byte e respondeu 200 no domínio
  administrativo. Seu caminho /client-demo.js respondeu 404. Somente o
  container do edge foi substituído, com os mesmos volumes ACME e Caddyfile.
- O rollback usa o container parado amparius-staging-https-pre-0f6a057,
  montado com preview-0d22237 e Caddyfile.a0e0ed7. Para restaurar, pare e
  renomeie o edge novo, devolva o nome amparius-staging-https ao anterior,
  inicie-o e repita os probes e a verificação pública dos arquivos.
