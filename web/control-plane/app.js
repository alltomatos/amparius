/* Runtime integration: assign window.AMPARIUS_CONFIG = { apiBase, accessToken } before this script. */
(() => {
  "use strict";

  const config = window.AMPARIUS_CONFIG || {};
  const apiBase = String(config.apiBase || "/").replace(/\/$/, "");
  const state = { companies: [], companyCursor: null, companyHistory: [], selectedCompany: null, entitlements: [], entitlementCursor: null, entitlementHistory: [] };
  const $ = (id) => document.getElementById(id);

  function setConnection(message, kind) {
    const node = $("connection");
    node.textContent = message;
    node.className = `status status-${kind}`;
  }

  function errorText(error) { return error && error.message ? error.message : "Não foi possível concluir a consulta."; }

  async function request(path, params = {}) {
    const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== null && value !== undefined && value !== ""));
    const response = await fetch(`${apiBase}${path}${query.size ? `?${query}` : ""}`, {
      headers: { Accept: "application/json", ...(config.accessToken ? { Authorization: `Bearer ${config.accessToken}` } : {}) },
      credentials: "same-origin",
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(payload.message || `Falha HTTP ${response.status}`);
      error.code = payload.code;
      error.traceId = payload.traceId;
      throw error;
    }
    return payload;
  }

  function showState(id, message, kind = "") { const node = $(id); node.textContent = message; node.className = `state-panel ${kind}`; }
  function displayDate(value) { return value ? new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium" }).format(new Date(value)) : "sem validade final"; }
  function setTrace(error) { $("trace").textContent = error && error.traceId ? `Rastreio: ${error.traceId}` : ""; }

  function renderCompanies() {
    const filter = $("company-search").value.trim().toLocaleLowerCase("pt-BR");
    const items = state.companies.filter((company) => (company.legalName || "").toLocaleLowerCase("pt-BR").includes(filter));
    $("companies").innerHTML = items.map((company) => `<button class="company-card ${state.selectedCompany?.id === company.id ? "selected" : ""}" data-company-id="${company.id}" aria-pressed="${state.selectedCompany?.id === company.id}"><div class="card-row"><span class="card-title">${escapeHtml(company.legalName)}</span><span class="company-status ${escapeHtml(company.status)}">${escapeHtml(company.status)}</span></div><p class="muted">CNPJ ${escapeHtml(company.cnpj || "mascarado")}</p><p class="muted">${escapeHtml(company.kind || "empresa")}</p></button>`).join("");
    if (!items.length) showState("companies-state", "Nenhuma empresa encontrada para este filtro.", "empty"); else showState("companies-state", `${items.length} empresa(s) no contexto autorizado.`, "");
    document.querySelectorAll("[data-company-id]").forEach((button) => button.addEventListener("click", () => selectCompany(button.dataset.companyId)));
  }

  function renderEntitlements() {
    $("selected-company").textContent = state.selectedCompany ? state.selectedCompany.legalName : "Selecione uma empresa";
    $("entitlements").innerHTML = state.entitlements.map((item) => { const used = Number(item.used || 0); const quota = Number(item.quota || 0); const percent = quota > 0 ? Math.min(100, Math.round((used / quota) * 100)) : 0; return `<article class="entitlement-card"><div class="card-row"><span class="card-title">${escapeHtml(item.moduleName || item.module)}</span><span class="license-status ${escapeHtml(item.status)}">${escapeHtml(item.status)}</span></div><p class="muted">${escapeHtml(item.kind || "módulo")} · válido até ${displayDate(item.validTo)}</p><div class="meter" aria-label="Uso ${used} de ${quota || "sem quota"}"><span style="width:${percent}%"></span></div><p class="muted">Uso: ${used} / ${quota || "sem quota configurada"}</p></article>`; }).join("");
  }

  async function loadContext() {
    try { const context = await request("/v1/context"); $("context-title").textContent = context.company?.legalName || "Contexto autenticado"; $("context-meta").textContent = `${context.tenant?.name || "Tenant"} · CNPJ ${context.company?.cnpj || "mascarado"}`; $("context-badge").textContent = context.company?.status || "ativo"; setConnection("SaaS online", "online"); } catch (error) { showState("companies-state", `${error.code || "ERRO"}: ${errorText(error)}`, "error"); setConnection("Contexto indisponível", "error"); setTrace(error); }
  }

  async function loadCompanies(cursor = null) {
    showState("companies-state", "Carregando empresas…");
    try { const payload = await request("/v1/companies", { limit: 12, cursor }); state.companies = payload.items || []; state.companyCursor = payload.nextCursor; renderCompanies(); renderPagination("companies-pagination", state.companyCursor, state.companyHistory, loadCompanies); } catch (error) { showState("companies-state", `${error.code || "ERRO"}: ${errorText(error)}`, "error"); setTrace(error); }
  }

  async function selectCompany(id) {
    state.selectedCompany = state.companies.find((company) => company.id === id) || null; state.entitlementHistory = []; state.entitlementCursor = null; renderCompanies(); showState("entitlements-state", "Carregando entitlements…");
    try { const payload = await request(`/v1/companies/${encodeURIComponent(id)}/entitlements`, { limit: 24 }); state.entitlements = payload.items || []; state.entitlementCursor = payload.nextCursor; showState("entitlements-state", state.entitlements.length ? `${state.entitlements.length} módulo(s) encontrado(s).` : "Nenhum módulo disponível para esta empresa.", state.entitlements.length ? "" : "empty"); renderEntitlements(); renderPagination("entitlements-pagination", state.entitlementCursor, state.entitlementHistory, () => loadEntitlements(id)); } catch (error) { showState("entitlements-state", `${error.code || "ERRO"}: ${errorText(error)}`, "error"); setTrace(error); }
  }

  async function loadEntitlements(id, cursor = null) { try { const payload = await request(`/v1/companies/${encodeURIComponent(id)}/entitlements`, { limit: 24, cursor }); state.entitlements = payload.items || []; state.entitlementCursor = payload.nextCursor; renderEntitlements(); renderPagination("entitlements-pagination", state.entitlementCursor, state.entitlementHistory, () => loadEntitlements(id)); } catch (error) { showState("entitlements-state", `${error.code || "ERRO"}: ${errorText(error)}`, "error"); } }
  function renderPagination(id, nextCursor, history, loader) { const node = $(id); node.innerHTML = `${history.length ? "<button data-page-back>Anterior</button>" : ""}${nextCursor ? "<button data-page-next>Próxima</button>" : ""}`; node.querySelector("[data-page-back]")?.addEventListener("click", () => loader(history.pop())); node.querySelector("[data-page-next]")?.addEventListener("click", () => { history.push(nextCursor); loader(nextCursor); }); }
  function escapeHtml(value) { return String(value ?? "").replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" }[character])); }

  $("company-search").addEventListener("input", renderCompanies);
  loadContext();
  loadCompanies();
})();
