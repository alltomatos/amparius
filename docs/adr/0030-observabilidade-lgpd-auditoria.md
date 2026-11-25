# ADR 0030 — Observabilidade sem PII, auditoria e evidência de privacidade

Status: aceito e implementado em 25/11/2026 — HARDEN-01B (#55)

## Contexto

O Amparius já possuía log JSON, métricas Prometheus e correlação por requisição,
mas não tinha um trace-id propagável nem uma trilha persistente para responder
quem fez qual ação dentro do tenant. O PRD também exige base legal, retenção e
trilha de consentimento para dados de familiares e falecidos.

## Decisões

1. `internal/platform/observability` gera e propaga um trace-id W3C opaco,
   aceita somente `traceparent` válido e expõe `X-Trace-Id`. Logs carregam
   `request_id` e `trace_id`; nenhum corpo, query string, token, subject, IP ou
   dado pessoal entra no access log.
2. Labels de métricas passam por uma guarda de PII. `tenant_id`, módulo,
   resultado e outros códigos operacionais são permitidos; e-mail, nome,
   documentos, telefone, segredo e valores longos são descartados.
3. `audit.log` é tenant-scoped, RLS, append-only e identifica o ator por
   `actor_user_id` quando há principal OIDC. A ação, recurso, resultado,
   empresa, correlação, trace, detalhe operacional e `retention_until` são
   persistidos; o detalhe não aceita PII nem texto livre.
4. `privacy.consent_record` registra uma ocorrência append-only por sujeito
   referenciado por UUID opaco, finalidade, `legal_basis_code`, estado,
   evidência opaca e `retention_until`. Retirada ou mudança cria nova
   ocorrência; não há atualização destrutiva.
5. Retenção é explícita e obrigatória no chamador. O código não inventa prazos,
   base legal ou anonimização: os códigos e a data devem vir da política
   aprovada pelo responsável de privacidade/DPO e pelo contexto jurídico. A
   migration não executa expurgo automático.
6. O registro de consentimento e seu evento de auditoria são gravados na mesma
   transação, evitando consentimento sem trilha ou trilha sem consentimento.

## Segurança e LGPD

As duas tabelas têm RLS por `app.tenant_id`, grants mínimos de
`SELECT, INSERT`, FK composta para usuário/empresa e triggers contra
`UPDATE/DELETE`. A aplicação valida o contexto tenant/company e impede que um
principal autenticado grave em nome de outro usuário. A trigger PostgreSQL
reutiliza a guarda de payload da outbox como segunda barreira contra PII.

## Consequências e limites

- A implementação fornece correlação de trace e observabilidade local, não um
  exportador OTLP nem spans distribuídos; a adoção de OpenTelemetry externo
  exige uma decisão de topologia e retenção própria.
- O pacote `audit` é uma API de plataforma e ainda precisa ser chamado pelos
  casos de uso que mudarem dados; o servidor não grava automaticamente uma
  linha para cada rota sem a transação do domínio.
- `retention_until` documenta a obrigação de retenção, mas job de expurgo,
  anonimização e fluxo de atendimento ao titular permanecem pendentes.
- Os testes usam somente fixtures sintéticas; nenhum código fiscal ou alíquota
  foi alterado.

## Verificação

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:<secret>@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:<secret>@127.0.0.1:5432/amparius?sslmode=disable'
go test ./internal/platform/audit ./internal/platform/observability ./internal/platform/http ./internal/platform/auth ./internal/modules/identity -count=1
go test ./internal/platform/audit -run TestAuditAndConsentRespectRLSAndPrivacy -count=1 -v
go vet ./...
go build ./...
git diff --check
```

O teste de integração exige `amparius_app` sem `SUPERUSER/BYPASSRLS`, prova
isolamento entre dois tenants e tenta inserir e-mail diretamente no detalhe;
sem os DSNs ele deve ser reportado como **não executado**, nunca como verde.
