# ADR 0003 — Multi-tenancy por coluna com RLS

Status: aceito · 21/11/2026

## Contexto

O SaaS atende várias empresas, algumas com filiais, todas no Brasil. Vazamento entre tenants é falha grave; banco por tenant multiplica custo operacional.

## Decisão

- **Um banco, schema por módulo, `tenant_id` em toda tabela de negócio.**
- **RLS obrigatória** com `current_setting('app.tenant_id')` definido por conexão/transação.
- `tenant_id` sempre vem do token/sessão, nunca do corpo da requisição.
- Filial (`branch_id`) é escopo secundário e entra nas políticas de acesso, não na RLS básica.
- Testes automatizados tentam ler dados de outro tenant e devem falhar.
- Migração para banco dedicado (tenant grande) é possível por exportação lógica, sem mudar o modelo.

## Consequências

- Uma query sem contexto de tenant falha; isso é intencional.
- Índices começam por `tenant_id`.
- Consultas administrativas usam papel com `BYPASSRLS` restrito e auditado.
- Backups e restauração são por banco; exportação por tenant é funcionalidade de produto.

## Critérios de aceite

1. Suíte de isolamento cobre todas as tabelas com `tenant_id`.
2. Nenhum endpoint aceita `tenant_id` do cliente.
3. Auditoria registra acesso administrativo cross-tenant.
