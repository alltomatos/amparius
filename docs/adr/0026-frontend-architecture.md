# ADR 0026 — Arquitetura das superfícies frontend

- **Status:** proposta para implementação sequencial
- **Data:** 2026-11-25
- **Escopo:** control plane web e cliente de operação/offline

## Contexto

O Amparius possui duas superfícies com objetivos e restrições diferentes:

1. o control plane administrativo, acessado por `manager.amparius.com`, fonte da verdade para tenants, empresas, licenças, quotas e auditoria;
2. o cliente de loja, acessado no ambiente operacional e sujeito a perda de conectividade, com fila local e reconciliação pelo relay.

Ainda não existe um workspace frontend no repositório. A especificação de produto está em `docs/design/frontend-spec.md`.

## Decisão

- O control plane será uma aplicação web separada do cliente operacional, consumindo contratos versionados `/v1` e autenticação OIDC/MFA conforme ADR 0024.
- O cliente operacional permanecerá no workspace Rust e terá uma camada de UI própria (`client/ui` quando iniciada), sem copiar componentes, CSS ou estrutura de outro ERP/repositório.
- O tenant será derivado do contexto autenticado. `X-Tenant-Id`, se usado para seleção visual, nunca será fonte de autorização.
- Escritas críticas usarão `Idempotency-Key`; alterações concorrentes usarão `version` e falharão com `STALE_VERSION`.
- O cliente tratará online, offline, contingência, fila pendente e conflito como estados distintos. Dinheiro e fiscal nunca usarão resolução silenciosa de conflito.
- SSE será somente mecanismo de invalidação/refetch; a API e o snapshot local validado continuam sendo a autoridade.
- Segredos, PII e payloads fiscais não serão exibidos em logs, capturas ou telemetria do frontend.

## Primeiras fatias verticais

### Control plane

`CP-01` login/contexto → `CP-05` listagem de entitlements → `CP-06` detalhe com concessão, revogação, quota, versão e auditoria.

Critério: um administrador autorizado consegue operar uma empresa dentro do tenant correto; usuário sem papel, empresa fora do tenant, licença bloqueada e versão obsoleta falham fechado.

### Cliente

`CL-01` painel do dia → `CL-02` atendimento → `CL-03` ordem de serviço com persistência local, `Idempotency-Key`, sincronização e revisão de conflito.

Critério: a operação permitida continua disponível offline, fica explicitamente enfileirada e não duplica ao reconectar; operações não suportadas offline informam o motivo.

## Consequências

- O backend precisa estabilizar os contratos HTTP antes da implementação visual definitiva.
- A entrega será sequencial por fatia, com QA de contrato e atualização do Userguide após cada aceite.
- O DNS deve manter separados `manager.amparius.com`, `app.amparius.com` e `hml.amparius.com.br`; a migração para Cloudflare não altera essa fronteira.

## Rejeitado

- Copiar o frontend de `D:\dev\allcode-go` ou de qualquer ERP.
- Colocar o control plane dentro do cliente offline.
- Usar `/api/session/active` ou `/api/event` sem contrato/ADR específico.
- Tratar indisponibilidade de DNS como motivo para desligar a operação offline.
