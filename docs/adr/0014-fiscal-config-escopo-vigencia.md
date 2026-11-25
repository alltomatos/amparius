# ADR 0014 — Configuração fiscal por empresa, estabelecimento e vigência

Status: aceito · 24/11/2026

## Contexto

O modelo fiscal já separava a configuração da empresa/CNPJ do tenant, mas
`fiscal.fiscal_config` ainda usava `company_id` como chave primária e não
registrava estabelecimento, inscrições nem o período em que uma configuração
era válida. Isso impedia manter histórico quando uma inscrição, ambiente ou
parâmetro operacional mudasse.

Certificados também precisam continuar sendo configurados por CNPJ sem que o
banco receba senha, PIN, chave privada, PFX, token ou bytes criptográficos.

## Decisão

- A migration forward-only `0019_fiscal_config_scope.sql` dá à configuração um
  `id` próprio e preserva `tenant_id` e `company_id` como escopos obrigatórios.
- `establishment_id` é opcional: `NULL` representa a configuração-padrão da
  empresa; quando preenchido, a FK composta garante que o estabelecimento
  pertence ao mesmo tenant e CNPJ.
- `valid_from` é inclusivo e `valid_to` é exclusivo. Uma exclusão GiST impede
  duas vigências sobrepostas para o mesmo tenant, empresa e estabelecimento.
- Inscrição estadual e inscrição municipal são metadados informados pelo
  contribuinte (`state_registration` e `municipal_registration`). Eles não
  representam alíquota, NCM, CST, cClassTrib, CFOP ou qualquer regra fiscal.
- `fiscal.certificate.storage_ref` continua sendo apenas referência opaca a
  Vault/KMS. Segredos não fazem parte do schema, do contrato Go ou dos logs.
- O papel `amparius_app` recebe somente `USAGE` já existente e
  `SELECT/INSERT/UPDATE` em `fiscal.fiscal_config`; não recebe `DELETE` direto.
  A migration `0020_fiscal_config_company_rls.sql` limita RLS ao
  `app.tenant_id` e ao `app.company_id`; o contrato Go exige ambos no contexto
  confiável.

## Consequências

- A mesma empresa pode ter uma configuração-padrão e configurações específicas
  por estabelecimento, desde que suas vigências não se sobreponham.
- Correções de estrutura devem ser novas migrations; `0004_fiscal.sql` não é
  editada.
- A consulta fail-closed da configuração vigente e o bloqueio da emissão sem
  configuração válida permanecem no escopo da FIS-01B.
- Nenhuma regra, alíquota ou código fiscal é semeado por esta migration.

## Critérios de aceite

1. A migration instala o novo escopo e suas constraints sem armazenar segredo.
2. Papel `NOSUPERUSER/NOBYPASSRLS` lê apenas configurações do tenant e empresa
   do contexto.
3. FK composta rejeita estabelecimento de outro CNPJ, e exclusão GiST rejeita
   vigências sobrepostas.
4. Teste de integração confirma isolamento e cleanup administrativo verificável.
