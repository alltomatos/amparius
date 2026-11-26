# ADR 0042 — Cadastro autenticado de falecido, vínculo familiar e consentimento

Status da [issue #73](https://github.com/alltomatos/amparius/issues/73) (E2E-01B2). Contrato implementado no backend. Consulte o [contrato HTTP v1](../contracts/party-deceased-family-link-v1.md). A operação em produção
depende do bootstrap OIDC/MFA (ADR 0038) e de política de privacidade aprovada
por tenant (ADR 0030).

## Contexto

O mockup Loja 02 inclui a seleção de falecido e responsável no atendimento.
O PRD RF-004, o DER §1 e o AGENTS.md §2.5 abrangem o cadastro de falecidos e
familiares. O ADR 0030 exige política aprovada para finalidade, base legal e
retenção e define a persistência de auditoria e evidência de consentimento.
Os registros de falecido e vínculo referenciam pessoas físicas existentes e
resolvem os nomes no cadastro de origem, conforme o ADR 0013.

## Decisões

1. As rotas autenticadas de criação e consulta usam `party:write` e
   `party:read`, respectivamente. A autenticação fornece tenant e empresa.
   O repositório valida as referências com RLS. IDs inexistentes ou de
   outro tenant devolvem `404 NOT_FOUND`.

2. Os dois POSTs exigem `audit.PolicyProvider`. O provedor resolve a política
   do tenant antes da transação, pelas chaves `party.deceased.register` e
   `party.family_link.register`. A política fornece finalidade, base legal e
   retenção. Provedor ausente, falha de resolução ou política inválida resulta
   em `503 PRIVACY_POLICY_UNAVAILABLE`. A configuração de políticas e o
   provedor de produção ficam pendentes.

3. `POST /v1/deceased` insere o falecido e chama `audit.AppendTx` na mesma
   transação. A ação `party.deceased.register` registra ID do recurso, ator,
   empresa, trace e retenção. O detalhe contém os códigos de finalidade e
   base legal da política. O corpo do cadastro não fornece esses códigos
   nem uma declaração de consentimento.

4. `POST /v1/deceased/{deceasedId}/family-links` exige um UUID não nulo que
   identifique o artefato externo de consentimento. `audit.RecordConsentTx`
   grava `privacy.consent_record` com estado `granted` para a pessoa vinculada
   e grava sua trilha de auditoria. Vínculo, consentimento e auditoria usam a
   mesma transação. Falha em uma gravação desfaz todas as gravações da
   operação. A API rejeita autovínculo comparando o `personId` recebido ao
   ID da pessoa referenciada pelo registro de falecido.

5. Cada criação adquire um bloqueio consultivo transacional do PostgreSQL
   por tenant, tipo de agregado e registro. O cadastro de falecido usa o
   ID da pessoa como chave. O vínculo usa o ID do registro de falecido,
   com checagem da dupla falecido e familiar. A leitura de existência é uma
   instrução separada após a aquisição do bloqueio. Em `READ COMMITTED`, ela
   enxerga o commit do concorrente anterior. O repositório rejeita a operação
   sob outro isolamento transacional para preservar essa garantia.
   Os caminhos dev e autenticado compartilham o bloqueio e a checagem.
   O mecanismo usa os grants existentes, sem nova migração. A garantia
   pressupõe que todo escritor use o repositório atualizado. Duplicatas
   legadas continuam consultáveis com seus IDs originais e impedem uma
   nova gravação para a mesma chave, com HTTP 409.

6. Com o bootstrap atual de `cmd/api`, as quatro rotas devolvem
   `503 AUTHENTICATION_UNAVAILABLE`, antes de atingir os handlers. Com um
   autenticador configurado, a ausência de política bloqueia os dois POSTs
   com `503 PRIVACY_POLICY_UNAVAILABLE`. As consultas dependem da
   autenticação e permissão de leitura.

## Limites

- Os caminhos `/_dev` mantêm os formatos de sucesso em modo de desenvolvimento
  e compartilham as validações de duplicidade e autovínculo.
- Listagem, atualização, remoção, verificação do artefato externo, retirada
  de consentimento e expurgo automático ficam fora desta fatia.
- A integração usa política sintética e duplos de OIDC/MFA com o serviço de
  autenticação, repositório e PostgreSQL reais.

## Verificação

A validação independente da [#73](https://github.com/alltomatos/amparius/issues/73)
passou em PostgreSQL 16.16 descartável no Goose 0035, com `amparius_app`
em `rolsuper=f` e `rolbypassrls=f`. Com os dois DSNs, passaram sete
integrações da #73, oito da #72, dois contratos dev e o teste de
auditoria/RLS. O pacote `party` levou 8,451 s. As seis contagens de
fixtures ficaram em zero, os triggers permaneceram habilitados e o
container e o volume foram removidos. A suíte geral sem DSNs, vet, build
e diff check passaram.

A [#82](https://github.com/alltomatos/amparius/issues/82) acompanha a
reconciliação isolada do rascunho 0036 no banco dev compartilhado antes de
outra migração. A validação da #73 usou a base descartável no Goose 0035.

Com PostgreSQL 16 descartável no Goose 0035, configure
`AMPA_TEST_POSTGRES_DSN` com o papel `amparius_app` sem `SUPERUSER` nem
`BYPASSRLS` e `AMPA_TEST_POSTGRES_ADMIN_DSN` para fixtures e limpeza.

```powershell
go test -p 1 ./cmd/api ./internal/modules/party ./internal/platform/audit -run 'TestBuildHandlerSobrevive|TestProduction|TestDevelopment(Deceased|FamilyLink)HTTPIntegration|TestAuditAndConsentRespectRLSAndPrivacy' -count=1 -v
# Executar a suíte geral sem os DSNs de teste.
go test -p 1 ./... -count=1
go vet ./...
go build ./...
git diff --check
```
