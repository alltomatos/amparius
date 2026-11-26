"use strict";

function createPanelDemo() {
  const tenant = { id: "tenant-demo", name: "Grupo de demonstração" };
  const company = { id: "empresa-demo", legalName: "Unidade Demonstração", cnpj: null, status: "active", kind: "Filial" };
  const core = { module: "identity", moduleName: "Identidade", kind: "core", status: "active" };
  let funeral = {
    module: "funeral", moduleName: "Funerária", kind: "optional", status: "active",
    quota: 5, used: 1, quotaConfigured: true, version: 1, validFrom: "2026-01-01",
  };
  const audit = [];
  const mutations = new Map();
  const base = `/v1/companies/${company.id}/entitlements`;
  const reply = (body, status = 200) => Response.json(body, { status });
  const page = (items) => reply({ items, nextCursor: null });

  return (path, { method, body, headers }) => {
    if (method === "GET") {
      switch (path) {
        case "/v1/context": return reply({ tenant, company });
        case "/v1/companies": return page([company]);
        case base: return page([core, funeral]);
        case `${base}/identity`: return reply(core);
        case `${base}/identity/audit`: return page([]);
        case `${base}/funeral`: return reply(funeral);
        case `${base}/funeral/audit`: return page(audit);
        default: return reply({ code: "NOT_FOUND", message: "Recurso fora da demonstração." }, 404);
      }
    }
    if (path !== `${base}/funeral` || !["PUT", "PATCH"].includes(method)) {
      return reply({ code: "NOT_FOUND", message: "Operação fora da demonstração." }, 404);
    }
    const key = headers["Idempotency-Key"];
    if (!key) return reply({ code: "INVALID_REQUEST", message: "Chave de idempotência obrigatória." }, 400);
    const replay = mutations.get(key);
    if (replay) {
      return replay.method === method && replay.body === body
        ? reply(replay.result)
        : reply({ code: "IDEMPOTENCY_CONFLICT", message: "Chave já usada com outro pedido." }, 409);
    }
    const { version, validFrom, validTo, clearValidTo, quota, status } = JSON.parse(body);
    if (version !== funeral.version) {
      return reply({ code: "STALE_VERSION", message: "Versão desatualizada. Selecione o módulo novamente." }, 409);
    }
    if (quota !== undefined && (!Number.isSafeInteger(quota) || quota < funeral.used)) {
      return reply({ code: "INVALID_REQUEST", message: "A cota deve cobrir o uso atual." }, 400);
    }
    const from = validFrom ?? funeral.validFrom;
    const to = clearValidTo ? null : validTo === undefined ? funeral.validTo : validTo;
    const date = /^\d{4}-\d{2}-\d{2}$/;
    if (!date.test(from) || (to && (!date.test(to) || to <= from))) {
      return reply({ code: "INVALID_REQUEST", message: "Informe um período válido." }, 400);
    }
    const at = new Date().toISOString();
    funeral = {
      ...funeral, validFrom: from, validTo: to, quota: quota ?? funeral.quota,
      status: method === "PUT" ? "active" : status ?? funeral.status,
      version: version + 1, updatedAt: at,
    };
    mutations.set(key, { method, body, result: funeral });
    const action = method === "PUT" ? "activated" : status ?? (quota === undefined ? "validity_updated" : "quota_updated");
    audit.unshift({ action, origin: "demonstração", actorId: "operador-demo", at });
    return reply(funeral);
  };
}

if (typeof module !== "undefined") module.exports = { createPanelDemo };
if (typeof window !== "undefined") window.AMPARIUS_CONFIG = { demo: createPanelDemo() };
