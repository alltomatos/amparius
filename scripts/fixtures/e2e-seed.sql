-- Amparius: fixtures sintéticas para o ambiente E2E local (issue #58 / E2E-01A).
-- Nenhum dado pessoal real: nomes, documentos e e-mails são fictícios
-- (AGENTS.md #5, LGPD). Idempotente: pode ser executado repetidas vezes sem
-- duplicar linhas, porque reaproveita a chave de onboarding e confere
-- existência antes de inserir catálogo.

\set ON_ERROR_STOP on

SELECT tenant_id, company_id, establishment_id, owner_user_id
FROM identity.create_onboarding(
    'e2e-fixture-funeraria-esperanca-v1',
    'Grupo Esperanca Fixture',
    'FIXTURE-TENANT-DOC-0001',
    'FIXTURE-CNPJ-0001',
    'Funeraria Esperanca Fixture Ltda',
    'funeral',
    'FIXTURE-CNPJ-0001-EST',
    'Funeraria Esperanca Fixture - Matriz',
    'Rua Fixture',
    '100',
    'Fortaleza',
    'CE',
    '60000000',
    'owner.fixture@amparius.invalid',
    'Pessoa Fixture Proprietaria'
);

-- +fixture: catálogo mínimo (item de serviço + lista de preço + preço vigente)
-- +fixture: na mesma empresa/tenant criados pelo onboarding acima.
DO $$
DECLARE
    v_tenant_id uuid;
    v_item_id uuid;
    v_list_id uuid;
BEGIN
    SELECT tenant_id INTO v_tenant_id
      FROM identity.onboarding_request
     WHERE onboarding_key = 'e2e-fixture-funeraria-esperanca-v1';

    IF v_tenant_id IS NULL THEN
        RAISE EXCEPTION 'fixture de onboarding nao encontrada';
    END IF;

    PERFORM set_config('app.tenant_id', v_tenant_id::text, true);

    SELECT id INTO v_item_id
      FROM catalog.catalog_item
     WHERE tenant_id = v_tenant_id AND name = 'Servico Fixture de Velorio';
    IF v_item_id IS NULL THEN
        INSERT INTO catalog.catalog_item (tenant_id, kind, name, unit)
        VALUES (v_tenant_id, 'service', 'Servico Fixture de Velorio', 'service')
        RETURNING id INTO v_item_id;
    END IF;

    SELECT id INTO v_list_id
      FROM catalog.price_list
     WHERE tenant_id = v_tenant_id AND name = 'Tabela Fixture Padrao';
    IF v_list_id IS NULL THEN
        INSERT INTO catalog.price_list (tenant_id, name, valid_from)
        VALUES (v_tenant_id, 'Tabela Fixture Padrao', CURRENT_DATE)
        RETURNING id INTO v_list_id;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM catalog.price
         WHERE tenant_id = v_tenant_id AND item_id = v_item_id AND list_id = v_list_id
    ) THEN
        INSERT INTO catalog.price (tenant_id, item_id, list_id, amount, valid_from)
        VALUES (v_tenant_id, v_item_id, v_list_id, 1800.00, CURRENT_DATE);
    END IF;
END $$;

-- Evidência legível do que a fixture deixou no banco.
SELECT 'tenant' AS fixture, t.id, t.name, t.document
  FROM identity.tenant t
  JOIN identity.onboarding_request o ON o.tenant_id = t.id
 WHERE o.onboarding_key = 'e2e-fixture-funeraria-esperanca-v1'
UNION ALL
SELECT 'company', c.id, c.name, c.document
  FROM identity.company c
  JOIN identity.onboarding_request o ON o.company_id = c.id
 WHERE o.onboarding_key = 'e2e-fixture-funeraria-esperanca-v1'
UNION ALL
SELECT 'catalog_item', i.id, i.name, i.unit
  FROM catalog.catalog_item i
  JOIN identity.onboarding_request o ON o.tenant_id = i.tenant_id
 WHERE o.onboarding_key = 'e2e-fixture-funeraria-esperanca-v1'
   AND i.name = 'Servico Fixture de Velorio';
