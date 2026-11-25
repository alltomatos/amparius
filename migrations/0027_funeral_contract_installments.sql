-- Amparius: plano de parcelas do contrato funerario e chave de idempotencia
-- para titulos recebidos por evento.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
CREATE TABLE funeral.contract_installment (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    contract_id uuid NOT NULL,
    number integer NOT NULL CHECK (number > 0),
    amount numeric(18,2) NOT NULL CHECK (amount >= 0),
    due_date date NOT NULL,
    status text NOT NULL DEFAULT 'scheduled' CHECK (status = 'scheduled'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contract_installment_tenant_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT contract_installment_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT contract_installment_contract_number_uq UNIQUE (tenant_id, contract_id, number)
);

CREATE INDEX contract_installment_tenant_contract_idx
    ON funeral.contract_installment (tenant_id, contract_id, number);

-- Snapshot append-only: a parcela de contrato nunca vira baixa financeira.
CREATE TRIGGER contract_installment_append_only
    BEFORE UPDATE OR DELETE ON funeral.contract_installment
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_mutation();

ALTER TABLE funeral.contract_installment ENABLE ROW LEVEL SECURITY;
CREATE POLICY contract_installment_tenant_isolation ON funeral.contract_installment
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
GRANT SELECT, INSERT ON funeral.contract_installment TO amparius_app;

-- O consumidor financeiro usa a chave do evento para não abrir dois títulos
-- para o mesmo contrato. Registros legados continuam válidos (NULL).
ALTER TABLE finance.title ADD COLUMN idempotency_key text;
ALTER TABLE finance.title ADD CONSTRAINT title_idempotency_key_length_ck
    CHECK (idempotency_key IS NULL OR (length(btrim(idempotency_key)) > 0 AND length(btrim(idempotency_key)) <= 200));
CREATE UNIQUE INDEX title_tenant_idempotency_uq
    ON finance.title (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS finance.title_tenant_idempotency_uq;
ALTER TABLE finance.title DROP CONSTRAINT IF EXISTS title_idempotency_key_length_ck;
ALTER TABLE finance.title DROP COLUMN IF EXISTS idempotency_key;
REVOKE SELECT, INSERT ON funeral.contract_installment FROM amparius_app;
DROP TABLE IF EXISTS funeral.contract_installment;
