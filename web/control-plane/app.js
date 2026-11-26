/* Runtime integration: assign window.AMPARIUS_CONFIG = { apiBase, accessToken } before this script. */
(() => {
  "use strict";
  const H = (window.AmpariusHelpers || {});
  const escapeHtml = H.escapeHtml || ((v) => String(v ?? ""));
  const config = window.AMPARIUS_CONFIG || {};
  const apiBase = String(config.apiBase || "/").replace(/\/$/, "");
  const state = { companies: [], companyCursor: null, companyHistory: [], selectedCompany: null, entitlements: [], entitlementCursor: null, entitlementHistory: [], selectedEntitlement: null, audit: [], auditCursor: null, auditHistory: [], detail: null, actionState: "" };
  const $ = (id) => document.getElementById(id);

  function setConnection(message, kind) {
    const node = $("connection");
    if (!node) return;
    node.textContent = message;
    node.className = `status status-${kind}`;
  }

  async function request(path, options = {}) {
    const method = options.method || "GET";
    const params = options.params || {};
    const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== null && value !== undefined && value !== ""));
    const headers = { Accept: "application/json", ...(config.accessToken ? { Authorization: `Bearer ${config.accessToken}` } : {}), ...(options.headers || {}) };
    const init = { method, headers, credentials: "same-origin" };
    if (options.body !== undefined) { headers["Content-Type"] = "application/json"; init.body = JSON.stringify(options.body); }
    const response = config.demo
      ? config.demo(path, init)
      : await fetch(`${apiBase}${path}${query.size ? `?${query}` : ""}`, init);
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const err = H.normalizeError ? H.normalizeError(payload, response.status) : { code: payload.code, message: payload.message, traceId: payload.traceId, details: payload.details };
      const error = new Error(err.message || `Falha HTTP ${response.status}`);
      error.code = err.code;
      error.traceId = err.traceId;
      error.details = err.details;
      error.status = response.status;
      throw error;
    }
    return payload;
  }

  function showState(id, message, kind = "") {
    const node = $(id);
    if (!node) return;
    node.textContent = message;
    node.className = `state-panel ${kind}`.trim();
  }

  function setTrace(error) { const node = $("trace"); if (node) node.textContent = error && error.traceId ? `Rastreio: ${error.traceId}` : ""; }

  function renderCompanies() {
    const filter = ($("company-search")?.value || "").trim().toLocaleLowerCase("pt-BR");
    const items = state.companies.filter((company) => (company.legalName || "").toLocaleLowerCase("pt-BR").includes(filter));
    $("companies").innerHTML = items.map((company) => `<button class="company-card ${state.selectedCompany?.id === company.id ? "selected" : ""}" data-company-id="${escapeHtml(company.id)}" aria-pressed="${state.selectedCompany?.id === company.id}"><div class="card-row"><span class="card-title">${escapeHtml(company.legalName)}</span><span class="company-status ${escapeHtml(company.status)}">${escapeHtml(company.status)}</span></div><p class="muted">CNPJ ${escapeHtml(H.displayCnpj ? H.displayCnpj(company.cnpj) : (company.cnpj || "mascarado"))}</p><p class="muted">${escapeHtml(company.kind || "empresa")}</p></button>`).join("");
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
      const selected = state.selectedEntitlement && state.selectedEntitlement.module === item.module ? "selected" : "";
      return `<button class="entitlement-card ${selected}" data-entitlement-module="${escapeHtml(item.module)}" aria-pressed="${selected === "selected"}"><div class="card-row"><span class="card-title">${escapeHtml(item.moduleName || item.module)}</span><span class="license-status ${escapeHtml(item.status)}">${escapeHtml(item.status)}</span></div><p class="muted">${escapeHtml(item.kind || "modulo")} - valido ate ${H.displayDate ? H.displayDate(item.validTo) : (item.validTo || "sem validade final")}</p><div class="meter" aria-label="Uso ${used} de ${quota || "sem quota"}"><span style="width:${percent}%"></span></div><p class="muted">Uso: ${used} / ${quota || "sem quota configurada"}</p></button>`;
    }).join("");
    document.querySelectorAll("[data-entitlement-module]").forEach((button) => button.addEventListener("click", () => selectEntitlement(button.dataset.entitlementModule)));
    if (!state.entitlements.length) showState("entitlements-state", "Nenhum modulo disponivel para esta empresa.", "empty");
  }

  async function loadContext() {
    try {
      const context = await request("/v1/context");
      $("context-title").textContent = context.company?.legalName || "Contexto autenticado";
      $("context-meta").textContent = `${context.tenant?.name || "Tenant"} - CNPJ ${H.displayCnpj ? H.displayCnpj(context.company?.cnpj) : (context.company?.cnpj || "mascarado")}`;
      $("context-badge").textContent = context.company?.status || "ativo";
      setConnection(config.demo ? "Demonstração · dados sintéticos" : "SaaS online", "online");
    } catch (error) {
      showState("companies-state", `${error.code || "ERRO"}: ${H.errorText ? H.errorText(error) : error.message}`, "error");
      setConnection("Contexto indisponivel", "error");
      setTrace(error);
    }
  }

  async function loadCompanies(cursor = null) {
    showState("companies-state", "Carregando empresas...");
    try {
      const payload = await request("/v1/companies", { params: { limit: 12, cursor } });
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
    state.selectedEntitlement = null;
    state.detail = null;
    state.audit = [];
    renderEntitlementDetail();
    renderCompanies();
    showState("entitlements-state", "Carregando entitlements...");
    await loadEntitlements(id);
  }

  async function loadEntitlements(id, cursor = null) {
    try {
      const payload = await request(`/v1/companies/${encodeURIComponent(id)}/entitlements`, { params: { limit: 24, cursor } });
      state.entitlements = payload.items || [];
      state.entitlementCursor = payload.nextCursor || null;
      showState("entitlements-state", state.entitlements.length ? `${state.entitlements.length} modulo(s) encontrado(s).` : "Nenhum modulo disponivel para esta empresa.", state.entitlements.length ? "" : "empty");
      renderEntitlements();
      renderEntitlementDetail();
      renderPagination("entitlements-pagination", state.entitlementCursor, state.entitlementHistory, (c) => loadEntitlements(id, c));
    } catch (error) {
      showState("entitlements-state", `${error.code || "ERRO"}: ${H.errorText ? H.errorText(error) : error.message}`, "error");
      setTrace(error);
    }
  }

  function selectEntitlement(module) {
    state.selectedEntitlement = state.entitlements.find((item) => item.module === module) || null;
    state.detail = null;
    state.audit = [];
    state.auditCursor = null;
    state.auditHistory = [];
    renderEntitlements();
    renderEntitlementDetail();
    if (state.selectedCompany && module) loadDetail(state.selectedCompany.id, module);
  }

  async function loadDetail(companyId, module) {
    showState("entitlement-detail-state", "Carregando detalhe...", "loading");
    try {
      const detail = await request(`/v1/companies/${encodeURIComponent(companyId)}/entitlements/${encodeURIComponent(module)}`);
      state.detail = detail;
      showState("entitlement-detail-state", "Detalhe carregado.", "");
      renderEntitlementDetail();
      await loadAudit(companyId, module);
    } catch (error) {
      state.detail = null;
      showState("entitlement-detail-state", `${H.formatError ? H.formatError(error) : (error.code || "ERRO") + ": " + error.message}`, "error");
      setTrace(error);
    }
  }

  async function loadAudit(companyId, module, cursor = null) {
    try {
      const payload = await request(`/v1/companies/${encodeURIComponent(companyId)}/entitlements/${encodeURIComponent(module)}/audit`, { params: { limit: 20, cursor } });
      state.audit = payload.items || [];
      state.auditCursor = payload.nextCursor || null;
      renderAudit();
      renderPagination("audit-pagination", state.auditCursor, state.auditHistory, (c) => loadAudit(companyId, module, c));
    } catch (error) {
      showState("audit-state", `${H.formatError ? H.formatError(error) : (error.code || "ERRO") + ": " + error.message}`, "error");
      setTrace(error);
    }
  }

  function renderAudit() {
    const node = $("audit-list");
    if (!node) return;
    if (!state.audit.length) { showState("audit-state", "Sem eventos de auditoria.", "empty"); node.innerHTML = ""; return; }
    showState("audit-state", `${state.audit.length} evento(s).`, "");
    node.innerHTML = state.audit.map((e) => `<li><strong>${escapeHtml(e.action)}</strong> - ${escapeHtml(e.origin || "")} - ${escapeHtml(H.displayDate ? H.displayDate(e.at) : (e.at || ""))}<br><span class="muted">${escapeHtml(e.actorId || "")}</span></li>`).join("");
  }

  function renderEntitlementDetail() {
    const item = state.selectedEntitlement;
    $("selected-entitlement").textContent = item ? (item.moduleName || item.module) : "Selecione um módulo";
    const detail = state.detail || item;
    if (!item) {
      $("entitlement-detail").innerHTML = "";
      showState("entitlement-detail-state", "Selecione uma licença para ver os detalhes.", "empty");
      const auditNode = $("audit-list"); if (auditNode) auditNode.innerHTML = "";
      return;
    }
    const used = Number(detail?.used || 0);
    const quota = Number(detail?.quota || 0);
    const quotaText = detail?.quotaConfigured ? `${used} / ${quota}` : "sem quota configurada";
    const version = H.parseVersion ? H.parseVersion(detail?.version) : (detail?.version || 0);
    const module = detail?.module || item.module;
    const core = module === "identity" || module === "core";
    $("entitlement-detail").innerHTML = `
      <dl class="detail-list">
        <div><dt>Modulo canonico</dt><dd>${escapeHtml(module)}</dd></div>
        <div><dt>Status/licenca</dt><dd>${escapeHtml(detail?.status || "")}</dd></div>
        <div><dt>Tipo</dt><dd>${escapeHtml(detail?.kind || "nao informado")}</dd></div>
        <div><dt>Valido de</dt><dd>${H.displayDate ? H.displayDate(detail?.validFrom) : (detail?.validFrom || "sem inicio")}</dd></div>
        <div><dt>Valido ate</dt><dd>${H.displayDate ? H.displayDate(detail?.validTo) : (detail?.validTo || "sem validade final")}</dd></div>
        <div><dt>Quota/uso</dt><dd>${escapeHtml(quotaText)}</dd></div>
        <div><dt>Versao</dt><dd>${escapeHtml(version)}</dd></div>
        <div><dt>Atualizado em</dt><dd>${H.displayDate ? H.displayDate(detail?.updatedAt) : (detail?.updatedAt || "nao informado")}</dd></div>
      </dl>
      <div class="detail-actions">
        <button type="button" data-action="grant" ${core ? "disabled aria-disabled=true" : ""}>Conceder/reativar (PUT)</button>
        <button type="button" data-action="revoke" ${core ? "disabled aria-disabled=true" : ""}>Revogar (PATCH)</button>
        <button type="button" data-action="quota" ${core ? "disabled aria-disabled=true" : ""}>Ajustar quota (PATCH)</button>
        <button type="button" data-action="validity" ${core ? "disabled aria-disabled=true" : ""}>Ajustar validade (PATCH)</button>
      </div>
      <p class="muted" id="action-result">${escapeHtml(state.actionState || "")}</p>
    `;
    document.querySelectorAll("[data-action]").forEach((btn) => btn.addEventListener("click", () => handleAction(btn.dataset.action, detail)));
    if (!state.detail) showState("entitlement-detail-state", "Detalhe somente leitura carregado a partir do contrato existente.", "");
  }

  async function handleAction(action, detail) {
    const company = state.selectedCompany;
    const module = detail?.module || state.selectedEntitlement?.module;
    if (!company || !module) return;
    const version = H.parseVersion ? H.parseVersion(detail?.version) : (detail?.version || 0);
    const isCore = module === "identity" || module === "core";
    if (isCore) { showState("entitlement-detail-state", "Modulo core nao pode ser alterado.", "error"); return; }
    try {
      if (action === "grant") {
        const validFrom = window.prompt("Valido de (YYYY-MM-DD):", new Date().toISOString().slice(0, 10));
        if (!validFrom) return;
        const validToRaw = window.prompt("Valido ate (YYYY-MM-DD ou vazio):", "");
        const validTo = validToRaw ? validToRaw : null;
        const key = H.buildIdempotencyKey ? H.buildIdempotencyKey("grant", module, Date.now()) : `grant-${module}-${Date.now()}`;
        showState("entitlement-detail-state", "Concedendo...", "loading");
        const res = await request(`/v1/companies/${encodeURIComponent(company.id)}/entitlements/${encodeURIComponent(module)}`, {
          method: "PUT",
          headers: { "Idempotency-Key": key },
          body: { validFrom, validTo, version },
        });
        state.detail = res;
        state.actionState = "Concessao/reativacao concluida com sucesso.";
        showState("entitlement-detail-state", "Concessao concluida.", "success");
      } else if (action === "revoke") {
        if (!window.confirm("Revogar licenca deste modulo?")) return;
        const key = H.buildIdempotencyKey ? H.buildIdempotencyKey("revoke", module, Date.now()) : `revoke-${module}-${Date.now()}`;
        showState("entitlement-detail-state", "Revogando...", "loading");
        const res = await request(`/v1/companies/${encodeURIComponent(company.id)}/entitlements/${encodeURIComponent(module)}`, {
          method: "PATCH",
          headers: { "Idempotency-Key": key },
          body: { status: "revoked", version },
        });
        state.detail = res;
        state.actionState = "Revogacao concluida com sucesso.";
        showState("entitlement-detail-state", "Revogacao concluida.", "success");
      } else if (action === "quota") {
        const qRaw = window.prompt("Nova quota (inteiro nao negativo):", String(detail?.quota || 0));
        if (qRaw === null) return;
        const quota = Number(qRaw);
        if (!Number.isFinite(quota) || quota < 0 || !Number.isInteger(quota)) { showState("entitlement-detail-state", "Quota invalida.", "error"); return; }
        const key = H.buildIdempotencyKey ? H.buildIdempotencyKey("quota", module, Date.now()) : `quota-${module}-${Date.now()}`;
        showState("entitlement-detail-state", "Ajustando quota...", "loading");
        const res = await request(`/v1/companies/${encodeURIComponent(company.id)}/entitlements/${encodeURIComponent(module)}`, {
          method: "PATCH",
          headers: { "Idempotency-Key": key },
          body: { quota, version },
        });
        state.detail = res;
        state.actionState = "Quota ajustada com sucesso.";
        showState("entitlement-detail-state", "Quota ajustada.", "success");
      } else if (action === "validity") {
        const validFrom = window.prompt("Valido de (YYYY-MM-DD ou vazio):", detail?.validFrom || "");
        const validToRaw = window.prompt("Valido ate (YYYY-MM-DD ou vazio):", detail?.validTo || "");
        const body = { version };
        if (validFrom) body.validFrom = validFrom;
        if (validToRaw === "") body.clearValidTo = true; else if (validToRaw) body.validTo = validToRaw;
        const key = H.buildIdempotencyKey ? H.buildIdempotencyKey("validity", module, Date.now()) : `validity-${module}-${Date.now()}`;
        showState("entitlement-detail-state", "Ajustando validade...", "loading");
        const res = await request(`/v1/companies/${encodeURIComponent(company.id)}/entitlements/${encodeURIComponent(module)}`, {
          method: "PATCH",
          headers: { "Idempotency-Key": key },
          body,
        });
        state.detail = res;
        state.actionState = "Validade ajustada com sucesso.";
        showState("entitlement-detail-state", "Validade ajustada.", "success");
      }
      if (config.demo) {
        const index = state.entitlements.findIndex((item) => item.module === module);
        if (index !== -1) state.entitlements[index] = state.detail;
        state.selectedEntitlement = state.detail;
        renderEntitlements();
      }
      renderEntitlementDetail();
      await loadAudit(company.id, module);
    } catch (error) {
      state.actionState = H.formatError ? H.formatError(error) : `${error.code || "ERRO"}: ${error.message}`;
      showState("entitlement-detail-state", state.actionState, "error");
      setTrace(error);
      renderEntitlementDetail();
    }
  }

  function renderPagination(id, nextCursor, history, loader) {
    const node = $(id);
    if (!node) return;
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

  const search = $("company-search");
  if (search) search.addEventListener("input", renderCompanies);
  loadContext();
  loadCompanies();
})();
