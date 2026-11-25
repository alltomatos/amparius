-- Amparius: guarda de payload da outbox (PII e tamanho) validada pelo banco.
-- Forward-only: corrige a regra da 0007 sem editar a migration original.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.assert_payload_safe(p_payload jsonb)
RETURNS void
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    v_max_bytes constant integer := 65536;
    v_max_string constant integer := 512;
BEGIN
    IF p_payload IS NULL OR jsonb_typeof(p_payload) <> 'object' THEN
        RAISE EXCEPTION 'payload da outbox deve ser um objeto JSON';
    END IF;
    IF octet_length(p_payload::text) > v_max_bytes THEN
        RAISE EXCEPTION 'payload da outbox excede % bytes', v_max_bytes;
    END IF;
    -- Chaves: token unico (name, cpf, email...) ou par vizinho (full_name,
    -- birth_date...). Chave terminada em id/ids/ref e referencia opaca e passa.
    IF EXISTS (
        WITH RECURSIVE walk(node) AS (
            SELECT p_payload
            UNION ALL
            SELECT child.value
            FROM walk w
            CROSS JOIN LATERAL (
                SELECT e.value
                FROM jsonb_each(CASE WHEN jsonb_typeof(w.node) = 'object' THEN w.node ELSE '{}'::jsonb END) AS e
                UNION ALL
                SELECT a.value
                FROM jsonb_array_elements(CASE WHEN jsonb_typeof(w.node) = 'array' THEN w.node ELSE '[]'::jsonb END) AS a
            ) AS child
        ), keys(key, tokens) AS (
            SELECT kv.key,
                string_to_array(
                    lower(regexp_replace(regexp_replace(kv.key, '([a-z0-9])([A-Z])', '\1 \2', 'g'), '[^A-Za-z0-9]+', ' ', 'g')),
                    ' '
                )
            FROM walk w
            CROSS JOIN LATERAL jsonb_each(CASE WHEN jsonb_typeof(w.node) = 'object' THEN w.node ELSE '{}'::jsonb END) AS kv
        )
        SELECT 1
        FROM keys
        WHERE cardinality(tokens) > 0
          AND tokens[cardinality(tokens)] NOT IN ('id', 'ids', 'ref')
          AND (
                tokens && ARRAY[
                    'name', 'cpf', 'cnpj', 'rg', 'ie',
                    'email', 'mail', 'phone', 'mobile', 'cellphone',
                    'telephone', 'telefone', 'fone', 'celular', 'whatsapp',
                    'address', 'street', 'logradouro', 'bairro', 'cidade',
                    'city', 'cep', 'zip', 'nascimento',
                    'obs', 'notes', 'observacao', 'message', 'mensagem',
                    'password', 'senha', 'token', 'secret', 'apikey',
                    'cartao', 'cvv', 'holder', 'titular'
                ]
                OR EXISTS (
                    SELECT 1
                    FROM generate_subscripts(tokens, 1) AS i
                    WHERE i > 1
                      AND (tokens[i - 1] || tokens[i]) = ANY (ARRAY[
                          'fullname', 'socialname', 'legalname', 'tradename',
                          'birthdate', 'datanascimento', 'zipcode', 'cardnumber',
                          'mothername', 'nomemae', 'cpfcnpj'
                      ])
                )
            )
    ) THEN
        RAISE EXCEPTION 'payload da outbox contem chave de dado pessoal proibida';
    END IF;
    -- Valores: e-mail, sequencia de 10-14 digitos sem letra (CPF/CNPJ/telefone)
    -- e texto livre acima do teto.
    IF EXISTS (
        WITH RECURSIVE walk(node) AS (
            SELECT p_payload
            UNION ALL
            SELECT child.value
            FROM walk w
            CROSS JOIN LATERAL (
                SELECT e.value
                FROM jsonb_each(CASE WHEN jsonb_typeof(w.node) = 'object' THEN w.node ELSE '{}'::jsonb END) AS e
                UNION ALL
                SELECT a.value
                FROM jsonb_array_elements(CASE WHEN jsonb_typeof(w.node) = 'array' THEN w.node ELSE '[]'::jsonb END) AS a
            ) AS child
        )
        SELECT 1
        FROM walk w
        WHERE jsonb_typeof(w.node) = 'string'
          AND (
                (w.node #>> '{}') ~* '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'
                OR (
                    (w.node #>> '{}') !~ '[A-Za-z]'
                    AND length(regexp_replace(w.node #>> '{}', '[^0-9]', '', 'g')) BETWEEN 10 AND 14
                )
                OR char_length(w.node #>> '{}') > v_max_string
            )
    ) THEN
        RAISE EXCEPTION 'payload da outbox contem valor de dado pessoal proibido';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Privilegios minimos: a guarda nao fica executavel por PUBLIC. O trigger roda
-- como o invocador, entao apenas o papel de aplicacao precisa de EXECUTE.
REVOKE ALL ON FUNCTION outbox.assert_payload_safe(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION outbox.assert_payload_safe(jsonb) TO amparius_app;

-- Reinstala validate_event com a checagem de payload no INSERT. O payload e
-- imutavel depois de criado, entao UPDATE nao repete a varredura.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.validate_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM outbox.assert_domain_value('outbox_event_status', NEW.status, 'status');
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'pending' THEN
            RAISE EXCEPTION 'outbox.event nasce pending, recebido %', NEW.status;
        END IF;
        IF NEW.attempts <> 0 OR NEW.processed_at IS NOT NULL THEN
            RAISE EXCEPTION 'outbox.event nasce sem tentativa e sem processed_at';
        END IF;
        PERFORM outbox.assert_payload_safe(NEW.payload);
        RETURN NEW;
    END IF;
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.aggregate_type <> OLD.aggregate_type
        OR NEW.aggregate_id <> OLD.aggregate_id OR NEW.event_type <> OLD.event_type
        OR NEW.event_version <> OLD.event_version OR NEW.payload <> OLD.payload
        OR NEW.idempotency_key <> OLD.idempotency_key OR NEW.max_attempts <> OLD.max_attempts
        OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'campos de identidade do outbox.event sao imutaveis';
    END IF;
    IF NEW.attempts < OLD.attempts OR NEW.attempts > OLD.attempts + 1 THEN
        RAISE EXCEPTION 'attempts avanca no maximo uma unidade por claim: % -> %', OLD.attempts, NEW.attempts;
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status = 'processing')
        OR (OLD.status = 'processing' AND NEW.status IN ('processing', 'processed', 'pending', 'failed'))
    ) THEN
        RAISE EXCEPTION 'transicao de status invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.validate_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM outbox.assert_domain_value('outbox_event_status', NEW.status, 'status');
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'pending' THEN
            RAISE EXCEPTION 'outbox.event nasce pending, recebido %', NEW.status;
        END IF;
        IF NEW.attempts <> 0 OR NEW.processed_at IS NOT NULL THEN
            RAISE EXCEPTION 'outbox.event nasce sem tentativa e sem processed_at';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.aggregate_type <> OLD.aggregate_type
        OR NEW.aggregate_id <> OLD.aggregate_id OR NEW.event_type <> OLD.event_type
        OR NEW.event_version <> OLD.event_version OR NEW.payload <> OLD.payload
        OR NEW.idempotency_key <> OLD.idempotency_key OR NEW.max_attempts <> OLD.max_attempts
        OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'campos de identidade do outbox.event sao imutaveis';
    END IF;
    IF NEW.attempts < OLD.attempts OR NEW.attempts > OLD.attempts + 1 THEN
        RAISE EXCEPTION 'attempts avanca no maximo uma unidade por claim: % -> %', OLD.attempts, NEW.attempts;
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status = 'processing')
        OR (OLD.status = 'processing' AND NEW.status IN ('processing', 'processed', 'pending', 'failed'))
    ) THEN
        RAISE EXCEPTION 'transicao de status invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP FUNCTION outbox.assert_payload_safe(jsonb);
