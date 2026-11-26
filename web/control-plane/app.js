/* Runtime integration: assign window.AMPARIUS_CONFIG = { apiBase, accessToken } before this script. */
(() => {
  "use strict";
  const H = (window.AmpariusHelpers || {});
  const escapeHtml = H.escapeHtml || ((v) => String(v ?? ""));
  const config = window.AMPARIUS_CONFIG || {};
  const apiBase = String(config.apiBase || "/").replace(/\/$/, "");
  const state = { companies: [], companyCursor: null, companyHistory: [], selectedCompany: null, entitlements: [], entitlementCursor: null, entitlementHistory: [] };
  const $ = (id) => document.getElementById(id);

  function setConnection(message, kind) {
    const node = $("connection");
    node.textContent = message;
    node.className = `status status-${kind}`;
  }

  async function request(path, params = {}) {
    const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== null && value !== undefined && value !== ""));
    const response = await fetch(`${apiBase}${path}${query.size ? `?${query}` : ""}`, {
      headers: { Accept: "application/json", ...(config.accessToken ? { Authorization: `Bearer ${config.accessToken}` } : {}) },
      credentials: "same-origin",
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const err = H.normalizeError ? H.normalizeError(payload, response.status) : { code: payload.code, message: payload.message, traceId: payload.traceId, details: payload.details };
      const error = new Error(err.message || `Falha HTTP ${response.status}`);
      error.code = err.code;
      error.traceId = err.traceId;
      error.details = err.details;
      throw error;
    }
    return payload;
  }

  function showState(id, message, kind = "") {
    const node = $(id);
    node.textContent = message;
    node.className = `state-panel ${kind}`.trim();
  }

  function setTrace(error) { $("trace").textContent = error && error.traceId ? `Rastreio: ${error.traceId}` : ""; }

  function renderCompanies() {
    const filter = $("company-search").value.trim().toLocaleLowerCase("pt-BR");
    const items = state.companies.filter((company) => (company.legalName || "").toLocaleLowerCase("pt-BR").includes(filter));
    $("companies").innerHTML = items.map((company) => `<button class="company-card ${state.selectedCompany?.id === company.id ? "selected" : ""}" data-company-id="${escapeHtml(company.id)}" aria-pressed="${state.selectedCompany?.id === company.id}"><div class="card-row"><span class="card-title">${escapeHtml(company.legalName)}</span><span class="company-status ${escapeHtml(company.status)}">${escapeHtml(company.status)}</span></div><p class="muted">CNPJ ${escapeHtml(H.maskCnpj ? H.maskCnpj(company.cnpj) : (company.cnpj || "mascarado"))}</p><p class="muted">${escapeHtml(company.kind || "empresa")}</p></button>`).join("");
    if (!items.length) showState("companies-state", "Nenhuma empresa encontrada para este filtro.", "empty");
    else showState("companies-state", `${items.length} empresa(s) no contexto autorizado.`, "");
    document.querySelectorAll("[data-company-id]").forEach((button) => button.addEventListener("click", () => selectCompany(button.dataset.companyId)));
  }

  function renderEntitlements() {
    $("selected-company").textContent = state.selectedCompany ? state.selectedCompany.legalName : "Selecione uma empresa";
    $("entitlements").innerHTML = state.entitlements.map((item) => {
      const used = Number(item.used || 0);
      const quota = Number(item.quota || 0);
      const percent = quota > 0 ? Math.min(100, Math.round((used / quota) * 100)) : 0;
      return `<article class="entitlement-card"><div class="card-row"><span class="card-title">${escapeHtml(item.moduleName || item.module)}</span><span class="license-status ${escapeHtml(item.status)}">${escapeHtml(item.status)}</span></div><p class="muted">${escapeHtml(item.kind || "modulo")} - valido ate ${H.displayDate ? H.displayDate(item.validTo) : (item.validTo || "sem validade final")}</p><div class="meter" aria-label="Uso ${used} de ${quota || "sem quota"}"><span style="width:${percent}%"></span></div><p class="muted">Uso: ${used} / ${quota || "sem quota configurada"}</p></article>`;
    }).join("");
    if (!state.entitlements.length) showState("entitlements-state", "Nenhum modulo disponivel para esta empresa.", "empty");
  }

  async function loadContext() {
    try {
      const context = await request("/v1/context");
      $("context-title").textContent = context.company?.legalName || "Contexto autenticado";
      $("context-meta").textContent = `${context.tenant?.name || "Tenant"} - CNPJ ${H.maskCnpj ? H.maskCnpj(context.company?.cnpj) : (context.company?.cnpj || "mascarado")}`;
      $("context-badge").textContent = context.company?.status || "ativo";
      setConnection("SaaS online", "online");
    } catch (error) {
      showState("companies-state", `${error.code || "ERRO"}: ${H.errorText ? H.errorText(error) : error.message}`, "error");
      setConnection("Contexto indisponivel", "error");
      setTrace(error);
    }
  }

  async function loadCompanies(cursor = null) {
    showState("companies-state", "Carregando empresas...");
    try {
      const payload = await request("/v1/companies", { limit: 12, cursor });
      state.companies = payload.items || [];
      state.companyCursor = payload.nextCursor || null;
      renderCompanies();
      renderPagination("companies-pagination", state.companyCursor, state.companyHistory, loadCompanies);
    } catch (error) {
      showState("companies-state", `${error.code || "ERRO"}: ${H.errorText ? H.errorText(error) : error.message}`, "error");
      setTrace(error);
    }
  }

  async function selectCompany(id) {
    state.selectedCompany = state.companies.find((company) => company.id === id) || null;
    state.entitlementHistory = [];
    state.entitlementCursor = null;
    state.entitlements = [];
    renderCompanies();
    showState("entitlements-state", "Carregando entitlements...");
    await loadEntitlements(id);
  }

  async function loadEntitlements(id, cursor = null) {
    try {
      const payload = await request(`/v1/companies/${encodeURIComponent(id)}/entitlements`, { limit: 24, cursor });
      state.entitlements = payload.items || [];
      state.entitlementCursor = payload.nextCursor || null;
      showState("entitlements-state", state.entitlements.length ? `${state.entitlements.length} modulo(s) encontrado(s).` : "Nenhum modulo disponivel para esta empresa.", state.entitlements.length ? "" : "empty");
      renderEntitlements();
      renderPagination("entitlements-pagination", state.entitlementCursor, state.entitlementHistory, (c) => loadEntitlements(id, c));
    } catch (error) {
      showState("entitlements-state", `${error.code || "ERRO"}: ${H.errorText ? H.errorText(error) : error.message}`, "error");
      setTrace(error);
    }
  }

  function renderPagination(id, nextCursor, history, loader) {
    const node = $(id);
    node.innerHTML = `${history.length ? '<button type="button" data-page-back>Anterior</button>' : ""}${nextCursor ? '<button type="button" data-page-next>Proxima</button>' : ""}`;
    node.querySelector("[data-page-back]")?.addEventListener("click", () => {
      const prev = history.pop();
      loader(prev);
    });
    node.querySelector("[data-page-next]")?.addEventListener("click", () => {
      history.push(nextCursor);
      loader(nextCursor);
    });
  }

  $("company-search").addEventListener("input", renderCompanies);
  loadContext();
  loadCompanies();
})();
EOF