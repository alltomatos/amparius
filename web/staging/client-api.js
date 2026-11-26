/* Cliente API da loja (Amparius).
 *
 * Consome a API Go da célula em dois modos, escolhidos pela presença dos
 * campos de `window.AMPARIUS_CONFIG`:
 *
 *   - `accessToken`  -> rotas autenticadas `/v1/...` (Authorization: Bearer);
 *   - `devTenantId`  -> rotas de desenvolvimento `/_dev/...` com o escopo de
 *                       tenant/empresa vindo dos cabeçalhos
 *                       X-Amparius-Development-Tenant-ID / -Company-ID
 *                       (APP_ENV=development + ENABLE_DEV_ONBOARDING).
 *
 * Sem nenhum dos dois, o cliente permanece no modo demonstração sintético e
 * este módulo não faz chamadas. As formas de sucesso e erro de produção e de
 * desenvolvimento são normalizadas para um formato único consumido pela UI.
 */
(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  }
  if (root) {
    root.AmpariusClientAPIFactory = api.createClientAPI;
    root.AmpariusClientAPI = api.createClientAPI(root.AMPARIUS_CONFIG || {});
  }
})(typeof self !== "undefined" ? self : (typeof globalThis !== "undefined" ? globalThis : this), function () {
  "use strict";

  const DEV_TENANT_HEADER = "X-Amparius-Development-Tenant-ID";
  const DEV_COMPANY_HEADER = "X-Amparius-Development-Company-ID";

  function isOfflineError(err) {
    return !!(err && err.code === "OFFLINE");
  }

  function statusToCode(status) {
    if (status === 0) return "OFFLINE";
    if (status === 401) return "AUTHENTICATION_REQUIRED";
    if (status === 403) return "PERMISSION_DENIED";
    if (status === 404) return "NOT_FOUND";
    if (status === 409) return "CONFLICT";
    if (status >= 500) return "UNAVAILABLE";
    return "HTTP_ERROR";
  }

  function normalizeError(payload, status) {
    const err = new Error("Falha na requisição");
    err.status = status;
    err.traceId = null;
    // Envelope de produção: { code, message, details, traceId }.
    if (payload && typeof payload === "object" && typeof payload.code === "string") {
      err.code = payload.code;
      err.message = payload.message || payload.code;
      err.traceId = payload.traceId || null;
      err.details = payload.details || {};
      return err;
    }
    // Envelope dev do middleware de escopo: { error: { code, message } }.
    if (payload && payload.error && typeof payload.error === "object" && typeof payload.error.code === "string") {
      err.code = payload.error.code;
      err.message = payload.error.message || payload.error.code;
      return err;
    }
    // Envelope dev dos handlers: { status: "error", error: "mensagem" }.
    if (payload && typeof payload.error === "string") {
      err.code = statusToCode(status);
      err.message = payload.error;
      return err;
    }
    err.code = statusToCode(status);
    return err;
  }

  function createClientAPI(config) {
    config = config || {};
    const apiBase = String(config.apiBase || "/").replace(/\/+$/, "");
    const accessToken = String(config.accessToken || "").trim();
    const devTenantId = String(config.devTenantId || "").trim();
    const devCompanyId = String(config.devCompanyId || "").trim();
    const fetchImpl = config.fetch || (typeof fetch !== "undefined" ? fetch : null);

    const mode = accessToken ? "bearer" : (devTenantId ? "dev" : null);
    const configured = mode !== null;

    function buildHeaders(extra) {
      const headers = Object.assign({ Accept: "application/json" }, extra || {});
      if (accessToken) headers.Authorization = "Bearer " + accessToken;
      if (devTenantId) headers[DEV_TENANT_HEADER] = devTenantId;
      if (devCompanyId) headers[DEV_COMPANY_HEADER] = devCompanyId;
      return headers;
    }

    function buildQuery(params) {
      const filtered = Object.entries(params || {}).filter(([, v]) => v !== null && v !== undefined && v !== "");
      if (filtered.length === 0) return "";
      return "?" + new URLSearchParams(filtered).toString();
    }

    async function request(path, options) {
      options = options || {};
      if (!fetchImpl) {
        const err = new Error("Sem transporte de rede disponível.");
        err.code = "OFFLINE";
        err.status = 0;
        throw err;
      }
      const method = options.method || "GET";
      const headers = buildHeaders();
      const init = { method, headers };
      if (options.body !== undefined && options.body !== null) {
        headers["Content-Type"] = "application/json";
        init.body = JSON.stringify(options.body);
      }
      let response;
      try {
        response = await fetchImpl(apiBase + path + buildQuery(options.query), init);
      } catch (cause) {
        const err = new Error("Sem conexão com o servidor.");
        err.code = "OFFLINE";
        err.status = 0;
        err.cause = cause;
        throw err;
      }
      let payload = {};
      try {
        payload = await response.json();
      } catch (_) {
        payload = {};
      }
      if (!response.ok) {
        throw normalizeError(payload, response.status);
      }
      return payload;
    }

    function pickItems(payload, keys) {
      for (const key of keys) {
        if (payload && Array.isArray(payload[key])) return payload[key];
      }
      return [];
    }

    function pickObject(payload, keys) {
      for (const key of keys) {
        if (payload && payload[key] && typeof payload[key] === "object") return payload[key];
      }
      return payload || {};
    }

    function partyFrom(raw) {
      const name = raw.display_name || raw.displayName || raw.civil_name || raw.civilName || raw.name || "";
      return { id: raw.id, name: name, document: raw.document || "" };
    }

    async function searchParties(q, opts) {
      opts = opts || {};
      const term = String(q || "").trim();
      if (!term) return [];
      const dev = mode === "dev";
      const path = dev ? "/_dev/parties" : "/v1/parties";
      const payload = await request(path, {
        query: { q: term, limit: opts.limit || (dev ? undefined : 25) },
      });
      const items = dev ? payload.parties || [] : payload.items || [];
      return items.map(partyFrom);
    }

    async function createParty(fields) {
      fields = fields || {};
      const dev = mode === "dev";
      const body = { name: fields.name };
      if (fields.document) body.document = fields.document;
      if (fields.socialName) body.social_name = fields.socialName;
      if (dev) body.kind = "person";
      const payload = await request(dev ? "/_dev/parties" : "/v1/parties", { method: "POST", body });
      return partyFrom(pickObject(payload, ["party"]));
    }

    async function createDeceased(fields) {
      fields = fields || {};
      const dev = mode === "dev";
      let path = dev ? "/_dev/deceased" : "/v1/deceased";
      let body;
      if (dev) {
        body = { person_id: fields.personId, death_at: fields.deathAt };
        if (fields.deathCertificate) body.death_certificate = fields.deathCertificate;
        if (fields.burialSite) body.burial_site = fields.burialSite;
      } else {
        body = { personId: fields.personId, deathAt: fields.deathAt };
        if (fields.deathCertificate) body.deathCertificate = fields.deathCertificate;
        if (fields.burialSite) body.burialSite = fields.burialSite;
      }
      const payload = await request(path, { method: "POST", body });
      const created = pickObject(payload, ["deceased"]);
      return { id: created.id, personId: created.person_id || created.personId || fields.personId };
    }

    async function createFamilyLink(fields) {
      fields = fields || {};
      const dev = mode === "dev";
      let path;
      let body;
      if (dev) {
        path = "/_dev/family-links";
        body = { deceased_id: fields.deceasedId, person_id: fields.personId, relation: fields.relation };
      } else {
        path = "/v1/deceased/" + encodeURIComponent(fields.deceasedId) + "/family-links";
        body = { personId: fields.personId, relation: fields.relation, consentEvidenceRef: fields.consentEvidenceRef };
      }
      const payload = await request(path, { method: "POST", body });
      const created = pickObject(payload, ["family_link"]);
      return { id: created.id, relation: created.relation || fields.relation };
    }

    async function searchServices(q, opts) {
      opts = opts || {};
      const term = String(q || "").trim();
      const dev = mode === "dev";
      const path = dev ? "/_dev/catalog/items" : "/v1/catalog/items";
      const payload = await request(path, {
        query: { q: term, limit: opts.limit || (dev ? undefined : 50) },
      });
      return pickItems(payload, ["items"]).map((raw) => ({
        id: raw.id,
        name: raw.name,
        unit: raw.unit,
      }));
    }

    async function searchPriceLists(on, opts) {
      opts = opts || {};
      const dev = mode === "dev";
      const path = dev ? "/_dev/catalog/price-lists" : "/v1/catalog/price-lists";
      const payload = await request(path, {
        query: { on: on || undefined, q: opts.q || undefined, limit: opts.limit || (dev ? undefined : 50) },
      });
      return pickItems(payload, ["price_lists", "items"]).map((raw) => ({
        id: raw.id,
        name: raw.name,
        validFrom: raw.valid_from || raw.validFrom || "",
      }));
    }

    return {
      configured,
      mode,
      apiBase,
      isOfflineError,
      normalizeError,
      searchParties,
      createParty,
      createDeceased,
      createFamilyLink,
      searchServices,
      searchPriceLists,
    };
  }

  return { createClientAPI };
});
