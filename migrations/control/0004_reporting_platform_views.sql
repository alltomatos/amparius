-- Amparius control plane: views de leitura cross-tenant do escopo de plataforma
-- (ADR 0040, decisao 3). Somente leitura: nenhum BYPASSRLS e nenhum JOIN em
-- codigo de modulo. As views pertencem ao dono das tabelas (que nao esta sujeito
-- a RLS porque nao ha FORCE ROW LEVEL SECURITY); o papel de aplicacao recebe
-- apenas SELECT nas views, nunca nas tabelas de outro tenant.
-- Sem PII: so ids opacos, codigos de modulo, contagens e estados.

-- +goose Up

CREATE SCHEMA IF NOT EXISTS reporting;

CREATE VIEW reporting.platform_license_pool AS
SELECT p.tenant_id,
       p.module_code,
       p.quota,
       (SELECT count(*)
          FROM entitlement.license_assignment a
         WHERE a.tenant_id = p.tenant_id
           AND a.module_code = p.module_code
           AND a.status = 'active'
           AND a.valid_from <= current_date
           AND (a.valid_to IS NULL OR a.valid_to >= current_date))::integer AS in_use,
       p.version,
       p.updated_at
  FROM entitlement.license_pool p;

CREATE VIEW reporting.platform_license_assignment AS
SELECT a.id,
       a.tenant_id,
       a.company_id,
       a.module_code,
       a.status,
       a.valid_from,
       a.valid_to,
       a.version,
       a.updated_at
  FROM entitlement.license_assignment a;

CREATE VIEW reporting.platform_entitlement_event AS
SELECT e.id,
       e.tenant_id,
       e.module_code,
       e.company_id,
       e.actor_id,
       e.action,
       e.origin,
       e."at"
  FROM entitlement.entitlement_event e;

CREATE VIEW reporting.platform_tenant_summary AS
SELECT t.tenant_id,
       COALESCE(p.modules_with_quota, 0)    AS modules_with_quota,
       COALESCE(p.quota_total, 0)           AS quota_total,
       COALESCE(a.assignments_total, 0)     AS assignments_total,
       COALESCE(a.assignments_active, 0)    AS assignments_active,
       u.seats                              AS user_seats,
       u.used                               AS user_used,
       e.last_event_at
  FROM (SELECT tenant_id FROM entitlement.license_pool
        UNION SELECT tenant_id FROM entitlement.license_assignment
        UNION SELECT tenant_id FROM entitlement.user_quota) t
  LEFT JOIN (SELECT tenant_id, count(*)::integer AS modules_with_quota, sum(quota)::integer AS quota_total
               FROM entitlement.license_pool GROUP BY tenant_id) p ON p.tenant_id = t.tenant_id
  LEFT JOIN (SELECT tenant_id,
                    count(*)::integer AS assignments_total,
                    (count(*) FILTER (WHERE status = 'active'
                                        AND valid_from <= current_date
                                        AND (valid_to IS NULL OR valid_to >= current_date)))::integer AS assignments_active
               FROM entitlement.license_assignment GROUP BY tenant_id) a ON a.tenant_id = t.tenant_id
  LEFT JOIN entitlement.user_quota u ON u.tenant_id = t.tenant_id
  LEFT JOIN (SELECT tenant_id, max("at") AS last_event_at
               FROM entitlement.entitlement_event GROUP BY tenant_id) e ON e.tenant_id = t.tenant_id;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'amparius_control_app') THEN
        GRANT USAGE ON SCHEMA reporting TO amparius_control_app;
        GRANT SELECT ON reporting.platform_license_pool,
                        reporting.platform_license_assignment,
                        reporting.platform_entitlement_event,
                        reporting.platform_tenant_summary
            TO amparius_control_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP VIEW IF EXISTS reporting.platform_tenant_summary;
DROP VIEW IF EXISTS reporting.platform_entitlement_event;
DROP VIEW IF EXISTS reporting.platform_license_assignment;
DROP VIEW IF EXISTS reporting.platform_license_pool;
