-- Amparius: espinha dorsal financeira.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS finance;

INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('finance_account_type', 'asset', 'Ativo'), ('finance_account_type', 'liability', 'Passivo'),
    ('finance_account_type', 'equity', 'Patrimonio liquido'), ('finance_account_type', 'revenue', 'Receita'),
    ('finance_account_type', 'expense', 'Despesa'), ('finance_account_status', 'active', 'Ativa'),
    ('finance_account_status', 'inactive', 'Inativa'), ('finance_cost_center_status', 'active', 'Ativo'),
    ('finance_cost_center_status', 'inactive', 'Inativo'), ('finance_bank_account_kind', 'checking', 'Conta corrente'),
    ('finance_bank_account_kind', 'savings', 'Poupanca'), ('finance_bank_account_kind', 'cash', 'Caixa'),
    ('finance_bank_account_status', 'active', 'Ativa'), ('finance_bank_account_status', 'inactive', 'Inativa'),
    ('finance_title_direction', 'receivable', 'A receber'), ('finance_title_direction', 'payable', 'A pagar'),
    ('finance_title_status', 'open', 'Aberto'), ('finance_title_status', 'partially_paid', 'Parcialmente pago'),
    ('finance_title_status', 'paid', 'Pago'), ('finance_title_status', 'cancelled', 'Cancelado'),
    ('finance_title_origin', 'sale', 'Venda'), ('finance_title_origin', 'purchase', 'Compra'),
    ('finance_title_origin', 'manual', 'Manual'), ('finance_installment_status', 'open', 'Aberta'),
    ('finance_installment_status', 'partially_paid', 'Parcialmente paga'), ('finance_installment_status', 'paid', 'Paga'),
    ('finance_installment_status', 'cancelled', 'Cancelada'), ('finance_payment_method', 'cash', 'Dinheiro'),
    ('finance_payment_method', 'pix', 'Pix'), ('finance_payment_method', 'bank_transfer', 'Transferencia bancaria'),
    ('finance_payment_method', 'card', 'Cartao'), ('finance_payment_method', 'boleto', 'Boleto'),
    ('finance_cash_session_status', 'open', 'Aberto'), ('finance_cash_session_status', 'closed', 'Fechado'),
    ('finance_cash_movement_kind', 'in', 'Entrada'), ('finance_cash_movement_kind', 'out', 'Saida')
ON CONFLICT (domain_code, code) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.assert_domain_value(p_domain_code text, p_code text, p_field_name text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF p_code IS NOT NULL AND NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code = p_domain_code AND code = p_code AND active) THEN
        RAISE EXCEPTION '% invalido para o dominio %: %', p_field_name, p_domain_code, p_code;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.validate_domains() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'account' THEN PERFORM finance.assert_domain_value('finance_account_type', NEW.type, 'type'); PERFORM finance.assert_domain_value('finance_account_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'cost_center' THEN PERFORM finance.assert_domain_value('finance_cost_center_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'bank_account' THEN PERFORM finance.assert_domain_value('finance_bank_account_kind', NEW.kind, 'kind'); PERFORM finance.assert_domain_value('finance_bank_account_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'title' THEN PERFORM finance.assert_domain_value('finance_title_direction', NEW.direction, 'direction'); PERFORM finance.assert_domain_value('finance_title_status', NEW.status, 'status'); PERFORM finance.assert_domain_value('finance_title_origin', NEW.origin, 'origin');
    ELSIF TG_TABLE_NAME = 'installment' THEN PERFORM finance.assert_domain_value('finance_installment_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'payment' THEN PERFORM finance.assert_domain_value('finance_payment_method', NEW.method, 'method');
    ELSIF TG_TABLE_NAME = 'cash_session' THEN PERFORM finance.assert_domain_value('finance_cash_session_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'cash_movement' THEN PERFORM finance.assert_domain_value('finance_cash_movement_kind', NEW.kind, 'kind'); END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE finance.account (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), parent_id uuid,
    code text NOT NULL, name text NOT NULL CHECK (length(btrim(name)) > 0), type text NOT NULL, status text NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_tenant_id_uq UNIQUE (tenant_id, id), CONSTRAINT account_tenant_parent_fk FOREIGN KEY (tenant_id, parent_id) REFERENCES finance.account(tenant_id, id), CONSTRAINT account_tenant_code_uq UNIQUE (tenant_id, code)
);
CREATE TABLE finance.cost_center (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), code text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0), status text NOT NULL, version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT cost_center_tenant_id_uq UNIQUE (tenant_id, id), CONSTRAINT cost_center_tenant_code_uq UNIQUE (tenant_id, code)
);
CREATE TABLE finance.bank_account (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), name text NOT NULL CHECK (length(btrim(name)) > 0),
    kind text NOT NULL, bank_code text, branch text, account_number text, status text NOT NULL, version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT bank_account_tenant_id_uq UNIQUE (tenant_id, id)
);
CREATE TABLE finance.title (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), direction text NOT NULL, party_id uuid,
    amount numeric(18,2) NOT NULL CHECK (amount >= 0), due_date date NOT NULL, status text NOT NULL, origin text NOT NULL, description text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT title_tenant_id_uq UNIQUE (tenant_id, id)
);
CREATE TABLE finance.installment (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), title_id uuid NOT NULL, number integer NOT NULL CHECK (number > 0),
    amount numeric(18,2) NOT NULL CHECK (amount >= 0), due_date date NOT NULL, status text NOT NULL, version integer NOT NULL DEFAULT 1 CHECK (version > 0), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT installment_tenant_title_fk FOREIGN KEY (tenant_id, title_id) REFERENCES finance.title(tenant_id, id), CONSTRAINT installment_tenant_id_uq UNIQUE (tenant_id, id), CONSTRAINT installment_title_number_uq UNIQUE (tenant_id, title_id, number)
);
CREATE TABLE finance.payment (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), installment_id uuid NOT NULL, bank_account_id uuid,
    amount numeric(18,2) NOT NULL CHECK (amount > 0), paid_at timestamptz NOT NULL DEFAULT now(), method text NOT NULL, reference text, created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payment_tenant_installment_fk FOREIGN KEY (tenant_id, installment_id) REFERENCES finance.installment(tenant_id, id), CONSTRAINT payment_tenant_bank_account_fk FOREIGN KEY (tenant_id, bank_account_id) REFERENCES finance.bank_account(tenant_id, id), CONSTRAINT payment_tenant_id_uq UNIQUE (tenant_id, id)
);
CREATE TABLE finance.cash_session (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), user_id uuid, opened_at timestamptz NOT NULL DEFAULT now(), closed_at timestamptz,
    opening_amount numeric(18,2) NOT NULL CHECK (opening_amount >= 0), status text NOT NULL, version integer NOT NULL DEFAULT 1 CHECK (version > 0), updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT cash_session_tenant_id_uq UNIQUE (tenant_id, id), CONSTRAINT cash_session_dates_ck CHECK (closed_at IS NULL OR closed_at >= opened_at)
);
CREATE TABLE finance.cash_movement (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), session_id uuid NOT NULL, kind text NOT NULL, amount numeric(18,2) NOT NULL CHECK (amount > 0), reason text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT cash_movement_tenant_session_fk FOREIGN KEY (tenant_id, session_id) REFERENCES finance.cash_session(tenant_id, id), CONSTRAINT cash_movement_tenant_id_uq UNIQUE (tenant_id, id)
);
CREATE TABLE finance.ledger_entry (
    id uuid NOT NULL DEFAULT public.uuidv7(), tenant_id uuid NOT NULL REFERENCES identity.tenant(id), account_id uuid NOT NULL, competence date NOT NULL,
    debit numeric(18,2) NOT NULL DEFAULT 0 CHECK (debit >= 0), credit numeric(18,2) NOT NULL DEFAULT 0 CHECK (credit >= 0), source text NOT NULL, source_id uuid, created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_entry_account_fk FOREIGN KEY (tenant_id, account_id) REFERENCES finance.account(tenant_id, id), CONSTRAINT ledger_entry_one_side_ck CHECK ((debit > 0 AND credit = 0) OR (credit > 0 AND debit = 0)), PRIMARY KEY (id, competence)
) PARTITION BY RANGE (competence);
CREATE TABLE finance.ledger_entry_2026_11 PARTITION OF finance.ledger_entry FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE finance.ledger_entry_2026_12 PARTITION OF finance.ledger_entry FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
CREATE TABLE finance.ledger_entry_default PARTITION OF finance.ledger_entry DEFAULT;

CREATE INDEX account_tenant_parent_idx ON finance.account (tenant_id, parent_id, id);
CREATE INDEX cost_center_tenant_idx ON finance.cost_center (tenant_id, id);
CREATE INDEX bank_account_tenant_idx ON finance.bank_account (tenant_id, id);
CREATE INDEX title_tenant_due_idx ON finance.title (tenant_id, due_date, id);
CREATE INDEX installment_tenant_title_idx ON finance.installment (tenant_id, title_id, number);
CREATE INDEX payment_tenant_installment_idx ON finance.payment (tenant_id, installment_id, paid_at);
CREATE INDEX cash_session_tenant_opened_idx ON finance.cash_session (tenant_id, opened_at, id);
CREATE INDEX cash_movement_tenant_session_idx ON finance.cash_movement (tenant_id, session_id, occurred_at);
CREATE INDEX ledger_entry_tenant_competence_idx ON finance.ledger_entry (tenant_id, competence, account_id);

CREATE TRIGGER account_validate_domains BEFORE INSERT OR UPDATE OF type, status ON finance.account FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER cost_center_validate_domains BEFORE INSERT OR UPDATE OF status ON finance.cost_center FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER bank_account_validate_domains BEFORE INSERT OR UPDATE OF kind, status ON finance.bank_account FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER title_validate_domains BEFORE INSERT OR UPDATE OF direction, status, origin ON finance.title FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER installment_validate_domains BEFORE INSERT OR UPDATE OF status ON finance.installment FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER payment_validate_domains BEFORE INSERT OR UPDATE OF method ON finance.payment FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER cash_session_validate_domains BEFORE INSERT OR UPDATE OF status ON finance.cash_session FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();
CREATE TRIGGER cash_movement_validate_domains BEFORE INSERT OR UPDATE OF kind ON finance.cash_movement FOR EACH ROW EXECUTE FUNCTION finance.validate_domains();

-- A soma e verificada sob lock da parcela, impedindo sobrepagamento por corrida.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.validate_payment_balance() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE installment_amount numeric(18,2); paid_amount numeric(18,2);
BEGIN
    SELECT amount INTO installment_amount FROM finance.installment WHERE id = NEW.installment_id FOR UPDATE;
    SELECT COALESCE(sum(amount), 0) INTO paid_amount FROM finance.payment WHERE installment_id = NEW.installment_id AND id <> COALESCE(NEW.id, '00000000-0000-0000-0000-000000000000'::uuid);
    IF paid_amount + NEW.amount > installment_amount THEN RAISE EXCEPTION 'pagamentos excedem o valor da parcela'; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER payment_balance_validate BEFORE INSERT OR UPDATE OF installment_id, amount ON finance.payment FOR EACH ROW EXECUTE FUNCTION finance.validate_payment_balance();

-- Ledger, pagamentos e movimentos de caixa preservam historico.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'tabela % e append-only: % proibido', TG_TABLE_NAME, TG_OP; END;
$$;
-- +goose StatementEnd
CREATE TRIGGER ledger_entry_append_only BEFORE UPDATE OR DELETE ON finance.ledger_entry FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();
CREATE TRIGGER payment_append_only BEFORE UPDATE OR DELETE ON finance.payment FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();
CREATE TRIGGER cash_movement_append_only BEFORE UPDATE OR DELETE ON finance.cash_movement FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();
CREATE TRIGGER title_history_preserve BEFORE DELETE ON finance.title FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();
CREATE TRIGGER installment_history_preserve BEFORE DELETE ON finance.installment FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();
CREATE TRIGGER cash_session_history_preserve BEFORE DELETE ON finance.cash_session FOR EACH ROW EXECUTE FUNCTION finance.reject_mutation();

-- +goose StatementBegin
DO $$
DECLARE table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['account','cost_center','bank_account','title','installment','payment','cash_session','cash_movement','ledger_entry'] LOOP
        EXECUTE format('ALTER TABLE finance.%I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY %I ON finance.%I USING (tenant_id = current_setting(''app.tenant_id'')::uuid) WITH CHECK (tenant_id = current_setting(''app.tenant_id'')::uuid)', table_name || '_tenant_isolation', table_name);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP SCHEMA finance CASCADE;
DELETE FROM identity.domain_value WHERE domain_code LIKE 'finance_%';
