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
`HEAD` de `/healthz` e `/readyz` para a API. Em `hml.amparius.com` ele
também serve dois fluxos públicos de demonstração com dados sintéticos:

- [Cliente web](https://hml.amparius.com/) — enfileirar um atendimento de
  exemplo, repetir a ação, simular reconexão e reenviar a mesma operação.
- [Painel SaaS](https://hml.amparius.com/painel/) — abrir a empresa de exemplo,
  selecionar **Funerária**, ajustar quota, conceder ou revogar licença e
  conferir a versão e auditoria de demonstração.

A fila e as mutações são isoladas por aba e reiniciam ao recarregar. A
política de conteúdo impede conexões da aplicação com API ou relay. A
versão `.com.br` continua com os probes públicos. Para testar rotas de
desenvolvimento com banco, abra o túnel a partir de uma estação autorizada:

```sh
ssh -N -L 127.0.0.1:18080:127.0.0.1:18080 omniroute
```

### Roteiro da prévia visual

- No cliente web, abra **Atendimento**, revise o cenário DEMO-001 e confirme
  duas vezes. A fila apresenta uma operação. Simule a reconexão e a
  sincronização. Repetir o envio preserva a mesma sequência.
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
curl -fsS https://hml.amparius.com/painel/ -o /dev/null
```

O resultado esperado é `200` nos dois probes e na prévia, e `404` em
`/_dev/` e `/v1/context`. No host,
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
