import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
const require = createRequire(import.meta.url);
const { createClientAPI } = require("./client-api.js");

function mockResponse(status, payload) {
  return {
    ok: status >= 200 && status < 300, status,
    json: async () => payload, text: async () => JSON.stringify(payload),
  };
}

function captureFetch(replies) {
  const calls = [];
  const fn = async (url, init) => {
    calls.push({ url, init });
    const next = replies.shift();
    if (next instanceof Error) throw next;
    return next;
  };
  fn.calls = calls;
  return fn;
}

test("mode bearer quando há accessToken", () => {
  const api = createClientAPI({ apiBase: "http://api", accessToken: "t1", fetch: () => {} });
  assert.equal(api.configured, true);
  assert.equal(api.mode, "bearer");
});

test("mode dev quando há devTenantId", () => {
  const api = createClientAPI({ devTenantId: "11111111-1111-1111-1111-111111111111", fetch: () => {} });
  assert.equal(api.configured, true);
  assert.equal(api.mode, "dev");
});

test("não configurado sem credenciais", () => {
  const api = createClientAPI({ apiBase: "http://api", fetch: () => {} });
  assert.equal(api.configured, false);
  assert.equal(api.mode, null);
});

test("searchParties bearer usa /v1/parties com Authorization e query", async () => {
  const fetch = captureFetch([
    mockResponse(200, { items: [{ id: "p1", kind: "person", name: "Pessoa", displayName: "Pessoa", document: "123", status: "active" }] }),
  ]);
  const api = createClientAPI({ apiBase: "http://api", accessToken: "tok", fetch });
  const result = await api.searchParties("Pessoa");
  assert.equal(fetch.calls[0].url, "http://api/v1/parties?q=Pessoa&limit=25");
  assert.equal(fetch.calls[0].init.headers.Authorization, "Bearer tok");
  assert.deepEqual(result, [{ id: "p1", name: "Pessoa", document: "123" }]);
});

test("searchParties dev usa /_dev/parties com header de tenant", async () => {
  const fetch = captureFetch([
    mockResponse(200, { status: "ok", parties: [{ id: "p1", civil_name: "Fulano", display_name: "Fulano", document: "" }] }),
  ]);
  const api = createClientAPI({ apiBase: "http://api", devTenantId: "T1", fetch });
  const result = await api.searchParties("Fulano");
  assert.equal(fetch.calls[0].url, "http://api/_dev/parties?q=Fulano");
  assert.equal(fetch.calls[0].init.headers["X-Amparius-Development-Tenant-ID"], "T1");
  assert.deepEqual(result, [{ id: "p1", name: "Fulano", document: "" }]);
});

test("searchParties com q vazio retorna lista vazia sem rede", async () => {
  const fetch = captureFetch([]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  const result = await api.searchParties("   ");
  assert.deepEqual(result, []);
  assert.equal(fetch.calls.length, 0);
});

test("busca sem resultados retorna lista vazia (estado vazio)", async () => {
  const fetch = captureFetch([mockResponse(200, { items: [] })]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  assert.deepEqual(await api.searchServices("nada"), []);
});

test("401 mapeia para AUTHENTICATION_REQUIRED", async () => {
  const fetch = captureFetch([mockResponse(401, { code: "AUTHENTICATION_REQUIRED", message: "sem token" })]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.searchParties("x"), (err) => {
    assert.equal(err.code, "AUTHENTICATION_REQUIRED");
    assert.equal(err.status, 401);
    return true;
  });
});

test("403 sem envelope mapeia para PERMISSION_DENIED", async () => {
  const fetch = captureFetch([mockResponse(403, {})]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.searchParties("x"), (err) => err.code === "PERMISSION_DENIED" && err.status === 403);
});

test("falha de rede mapeia para OFFLINE", async () => {
  const fetch = captureFetch([new Error("network down")]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.searchParties("x"), (err) => {
    assert.equal(err.code, "OFFLINE");
    assert.equal(err.status, 0);
    assert.equal(api.isOfflineError(err), true);
    return true;
  });
});

test("createParty bearer envia corpo correto em /v1/parties", async () => {
  const fetch = captureFetch([
    mockResponse(201, { id: "p1", name: "Ana", document: "111", status: "active" }),
  ]);
  const api = createClientAPI({ apiBase: "http://api", accessToken: "tok", fetch });
  const created = await api.createParty({ name: "Ana", document: "111", socialName: "Aninha" });
  assert.equal(fetch.calls[0].url, "http://api/v1/parties");
  assert.equal(fetch.calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { name: "Ana", document: "111", social_name: "Aninha" });
  assert.deepEqual(created, { id: "p1", name: "Ana", document: "111" });
});

test("createParty dev força kind=person e desembrulha party", async () => {
  const fetch = captureFetch([
    mockResponse(201, { status: "ok", party: { id: "p1", name: "Ana", civil_name: "Ana", display_name: "Ana", document: "" } }),
  ]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  const created = await api.createParty({ name: "Ana" });
  assert.equal(fetch.calls[0].url, "/_dev/parties");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { name: "Ana", kind: "person" });
  assert.equal(created.id, "p1");
});

test("createDeceased dev envia snake_case para /_dev/deceased", async () => {
  const fetch = captureFetch([
    mockResponse(201, { deceased: { id: "d1", person_id: "p1", death_at: "2026-11-20T10:00:00Z" } }),
  ]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  const created = await api.createDeceased({ personId: "p1", deathAt: "2026-11-20T10:00:00Z" });
  assert.equal(fetch.calls[0].url, "/_dev/deceased");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { person_id: "p1", death_at: "2026-11-20T10:00:00Z" });
  assert.deepEqual(created, { id: "d1", personId: "p1" });
});

test("createFamilyLink bearer usa consentEvidenceRef no path de produção", async () => {
  const fetch = captureFetch([
    mockResponse(201, { id: "f1", relation: "child" }),
  ]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  const created = await api.createFamilyLink({ deceasedId: "d1", personId: "p1", relation: "child", consentEvidenceRef: "c1" });
  assert.equal(fetch.calls[0].url, "/v1/deceased/d1/family-links");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { personId: "p1", relation: "child", consentEvidenceRef: "c1" });
  assert.equal(created.id, "f1");
});

test("createFamilyLink dev envia deceased_id para /_dev/family-links", async () => {
  const fetch = captureFetch([
    mockResponse(201, { status: "ok", family_link: { id: "f1", relation: "child" } }),
  ]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  const created = await api.createFamilyLink({ deceasedId: "d1", personId: "p1", relation: "child" });
  assert.equal(fetch.calls[0].url, "/_dev/family-links");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { deceased_id: "d1", person_id: "p1", relation: "child" });
  assert.equal(created.id, "f1");
});

test("searchServices dev desembrulha items de /_dev/catalog/items", async () => {
  const fetch = captureFetch([
    mockResponse(200, { status: "ok", items: [{ id: "s1", name: "Missa", unit: "service" }] }),
  ]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  const result = await api.searchServices("Missa");
  assert.equal(fetch.calls[0].url, "/_dev/catalog/items?q=Missa");
  assert.deepEqual(result, [{ id: "s1", name: "Missa", unit: "service" }]);
});

test("searchPriceLists dev desembrulha price_lists", async () => {
  const fetch = captureFetch([
    mockResponse(200, { status: "ok", price_lists: [{ id: "l1", name: "Tabela", valid_from: "2026-11-10" }] }),
  ]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  const result = await api.searchPriceLists("2026-11-10");
  assert.deepEqual(result, [{ id: "l1", name: "Tabela", validFrom: "2026-11-10" }]);
});

test("erro dev com mensagem em string mapeia pelo status HTTP", async () => {
  const fetch = captureFetch([mockResponse(409, { status: "error", error: "familiar já vinculado" })]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  await assert.rejects(api.createFamilyLink({ deceasedId: "d1", personId: "p1", relation: "child" }), (err) => {
    assert.equal(err.code, "CONFLICT");
    assert.equal(err.status, 409);
    assert.match(err.message, /vinculado/);
    return true;
  });
});

const PJ = { id: "c1", kind: "company", legalName: "Floricultura Exemplo Ltda", cnpj: "11222333000181", tradeName: "Exemplo", stateRegistration: null, municipalRegistration: null, status: "active" };

test("createLegalEntity bearer usa /v1/legal-entities sem tenant no corpo", async () => {
  const fetch = captureFetch([mockResponse(201, PJ)]);
  const api = createClientAPI({ apiBase: "http://api", accessToken: "tok", fetch });
  const created = await api.createLegalEntity({ legalName: "Floricultura Exemplo Ltda", cnpj: "11.222.333/0001-81", tradeName: "Exemplo" });
  assert.equal(fetch.calls[0].url, "http://api/v1/legal-entities");
  assert.equal(fetch.calls[0].init.method, "POST");
  assert.equal(fetch.calls[0].init.headers.Authorization, "Bearer tok");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { legalName: "Floricultura Exemplo Ltda", cnpj: "11.222.333/0001-81", tradeName: "Exemplo" });
  assert.equal(created.id, "c1");
  assert.equal(created.cnpj, "11222333000181");
  assert.equal(created.stateRegistration, "");
});

test("createLegalEntity dev usa /_dev/legal-entities com header de tenant", async () => {
  const fetch = captureFetch([mockResponse(201, PJ)]);
  const api = createClientAPI({ devTenantId: "T1", fetch });
  await api.createLegalEntity({ legalName: "X", cnpj: "11222333000181" });
  assert.equal(fetch.calls[0].url, "/_dev/legal-entities");
  assert.equal(fetch.calls[0].init.headers["X-Amparius-Development-Tenant-ID"], "T1");
  assert.deepEqual(Object.keys(JSON.parse(fetch.calls[0].init.body)).sort(), ["cnpj", "legalName"]);
});

test("createLegalEntity propaga 409 de CNPJ duplicado com traceId", async () => {
  const fetch = captureFetch([mockResponse(409, { code: "LEGAL_ENTITY_ALREADY_EXISTS", message: "CNPJ já cadastrado", traceId: "tr1" })]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.createLegalEntity({ legalName: "X", cnpj: "11222333000181" }), (err) => {
    assert.equal(err.status, 409);
    assert.equal(err.code, "LEGAL_ENTITY_ALREADY_EXISTS");
    assert.equal(err.traceId, "tr1");
    return true;
  });
});

test("searchLegalEntities bearer consulta /v1/legal-entities; vazio não usa rede", async () => {
  const fetch = captureFetch([mockResponse(200, { items: [PJ] })]);
  const api = createClientAPI({ apiBase: "http://api", accessToken: "tok", fetch });
  const result = await api.searchLegalEntities("Exemplo");
  assert.equal(fetch.calls[0].url, "http://api/v1/legal-entities?q=Exemplo&limit=25");
  assert.equal(result[0].legalName, "Floricultura Exemplo Ltda");
  assert.deepEqual(await api.searchLegalEntities("  "), []);
  assert.equal(fetch.calls.length, 1);
});

test("uma nova instância dev busca a PJ por nome ou CNPJ usando o tenant", async () => {
  const fetch = captureFetch([mockResponse(201, PJ), mockResponse(200, { items: [PJ] }), mockResponse(200, { items: [PJ] })]);
  const config = { devTenantId: "T1", fetch };
  const created = await createClientAPI(config).createLegalEntity(PJ);
  const fresh = createClientAPI(config);

  for (const term of [created.legalName, "11.222.333/0001-81"]) {
    assert.deepEqual(await fresh.searchLegalEntities(term, { limit: 10 }), [created]);
    const { url, init } = fetch.calls.at(-1);
    assert.equal(url, "/_dev/legal-entities?" + new URLSearchParams({ q: term, limit: 10 }));
    assert.equal(init.method, "GET");
    assert.equal(init.headers["X-Amparius-Development-Tenant-ID"], "T1");
    assert.equal(init.headers.Authorization, undefined);
  }
  assert.deepEqual(await fresh.searchLegalEntities("  "), []);
  assert.equal(fetch.calls.length, 3);
});

test("getLegalEntity monta o agregado com contatos, endereços e representantes", async () => {
  const fetch = captureFetch([mockResponse(200, Object.assign({}, PJ, {
    contacts: [{ id: "k1", partyId: "c1", kind: "email", value: "contato@exemplo.test", primary: true }],
    addresses: [{ id: "a1", partyId: "c1", street: "Rua Um", number: "10", city: "Fortaleza", state: "CE", zip: "60000000", primary: true }],
    representatives: [{ id: "r1", companyId: "c1", personId: "p1", role: "legal_representative", civilName: "Maria Civil", socialName: null, displayName: "Maria Civil" }],
  }))]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  const entity = await api.getLegalEntity("c1");
  assert.equal(fetch.calls[0].url, "/v1/legal-entities/c1");
  assert.equal(entity.contacts[0].primary, true);
  assert.equal(entity.addresses[0].zip, "60000000");
  assert.deepEqual(entity.representatives[0], { id: "r1", companyId: "c1", personId: "p1", role: "legal_representative", name: "Maria Civil" });
});

test("getLegalEntity 404 e id vazio", async () => {
  const fetch = captureFetch([mockResponse(404, { code: "LEGAL_ENTITY_NOT_FOUND", message: "não encontrada" })]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.getLegalEntity("c9"), (err) => err.status === 404 && err.code === "LEGAL_ENTITY_NOT_FOUND");
  await assert.rejects(api.getLegalEntity(" "), (err) => err.code === "VALIDATION_ERROR");
  assert.equal(fetch.calls.length, 1);
});

test("addContact e addAddress usam /v1/parties/{id} e /_dev/parties/{id}", async () => {
  const fetch = captureFetch([
    mockResponse(201, { id: "k1", partyId: "c1", kind: "phone", value: "85999990000", primary: false }),
    mockResponse(201, { id: "a1", partyId: "c1", street: "Rua Um", number: "10", city: "Fortaleza", state: "CE", zip: "60000000", primary: true }),
  ]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  const contact = await api.addContact("c1", { kind: "phone", value: "(85) 99999-0000" });
  assert.equal(fetch.calls[0].url, "/v1/parties/c1/contacts");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { kind: "phone", value: "(85) 99999-0000" });
  assert.equal(contact.primary, false);
  const address = await api.addAddress("c1", { street: "Rua Um", number: "10", city: "Fortaleza", state: "CE", zip: "60000-000", primary: true });
  assert.equal(fetch.calls[1].url, "/v1/parties/c1/addresses");
  assert.equal(JSON.parse(fetch.calls[1].init.body).primary, true);
  assert.equal(address.state, "CE");

  const devFetch = captureFetch([mockResponse(201, { id: "k2", partyId: "c1", kind: "email", value: "a@b.test", primary: false })]);
  const dev = createClientAPI({ devTenantId: "T1", fetch: devFetch });
  await dev.addContact("c1", { kind: "email", value: "a@b.test" });
  assert.equal(devFetch.calls[0].url, "/_dev/parties/c1/contacts");
});

test("addRepresentative envia personId e role; 503 de política indisponível vira UNAVAILABLE", async () => {
  const fetch = captureFetch([
    mockResponse(201, { id: "r1", companyId: "c1", personId: "p1", role: "administrator", civilName: "Maria", socialName: null, displayName: "Maria" }),
    mockResponse(503, { code: "PRIVACY_POLICY_UNAVAILABLE", message: "política indisponível", traceId: "t9" }),
  ]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  const rep = await api.addRepresentative("c1", { personId: "p1", role: "administrator" });
  assert.equal(fetch.calls[0].url, "/v1/legal-entities/c1/representatives");
  assert.deepEqual(JSON.parse(fetch.calls[0].init.body), { personId: "p1", role: "administrator" });
  assert.equal(rep.name, "Maria");
  await assert.rejects(api.addRepresentative("c1", { personId: "p1", role: "contact" }), (err) => err.status === 503 && err.code === "PRIVACY_POLICY_UNAVAILABLE");
});

test("falha de rede em createLegalEntity vira OFFLINE", async () => {
  const fetch = captureFetch([new Error("rede")]);
  const api = createClientAPI({ accessToken: "tok", fetch });
  await assert.rejects(api.createLegalEntity({ legalName: "X", cnpj: "11222333000181" }), (err) => api.isOfflineError(err));
});

// Exercita os handlers de client-live com eventos e respostas controlados.
function mountLiveClient(methods, { attendance = false, location = {}, dashboard = false, config, fetch } = {}) {
  const byId = new Map();
  const windowEvents = new Map();
  function element(tag) {
    return {
      children: [],
      value: "",
      disabled: false,
      set id(value) { byId.set(value, this); this.elementId = value; },
      get id() { return this.elementId; },
      get firstElementChild() { return this.children[0]; },
      attributes: {},
      setAttribute(key, value) { this.attributes[key] = value; },
      removeAttribute(key) { delete this.attributes[key]; },
      classList: { toggle() {} },
      focus() {},
      append(...children) { this.children.push(...children); },
      appendChild(child) { this.append(child); },
      replaceChildren(...children) {
        this.children = children;
        if (tag === "select") this.value = children[0]?.value || "";
      },
      addEventListener(event, handler) { this[event] = handler; },
    };
  }
  const section = element("section");
  const attendanceSection = attendance ? element("section") : null;
  const screens = new Map([["cadastros", section], ["atendimento", attendanceSection]]);
  const nav = [];
  for (const id of ["connection", "feedback"]) element("p").id = id;
  if (dashboard) {
    const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
    for (const [, tag, attrs, id, rest] of html.matchAll(/<(\w+)\b([^>]*?)\bid="([^"]+)"([^>]*)>/g)) {
      const node = element(tag);
      node.id = id;
      node.hidden = /\bhidden\b/.test(attrs + rest);
    }
    for (const [, name, label] of html.matchAll(/<a href="#([^"]+)"[^>]*>([^<]+)<\/a>/g)) {
      if (screens.get(name)?.dataset || !html.includes('data-screen="' + name + '"')) continue;
      const screen = screens.get(name) || element("section");
      screen.dataset = { screen: name };
      screens.set(name, screen);
      nav.push({ ...element("a"), hash: "#" + name, textContent: label });
    }
    byId.get("record-kind").value = "person";
    byId.get("private-day").parentElement = screens.get("painel");
    byId.get("private-finance").parentElement = screens.get("financeiro");
    location.hash ||= "";
  }
  const document = {
    createElement: element,
    getElementById: (id) => byId.get(id),
    querySelector: (selector) => screens.get(selector.match(/"([^"]+)"/)?.[1]),
    querySelectorAll: (selector) => selector === "[data-screen]" ? [...screens.values()] : nav,
  };
  const api = dashboard ? createClientAPI({ fetch, ...config }) : { configured: true, mode: "dev", isOfflineError: () => false };
  const env = {
    document, location,
    Option: function (textContent, value) { return { textContent, value }; },
    window: {
      location,
      AMPARIUS_CONFIG: config,
      AmpariusClientAPI: { ...api, ...methods },
      addEventListener(name, listener) {
        if (!windowEvents.has(name)) windowEvents.set(name, []);
        windowEvents.get(name).push(listener);
      },
    },
  };
  if (dashboard) runInNewContext(readFileSync(new URL("./client-demo.js", import.meta.url), "utf8"), env);
  runInNewContext(readFileSync(new URL("./client-live.js", import.meta.url), "utf8"), env);
  if (dashboard) {
    return {
      get: document.getElementById,
      async navigate(hash) {
        location.hash = hash;
        await Promise.all(windowEvents.get("hashchange").map((listener) => listener()));
      },
    };
  }
  return document.getElementById;
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function detail(entity) {
  return { ...entity, contacts: [], addresses: [], representatives: [] };
}

test("client-live ignora busca e detalhe antigos e distingue erro de vazio", async () => {
  const slowSearch = deferred();
  const slowDetail = deferred();
  const newer = { ...PJ, id: "pj2", legalName: "Segunda PJ" };
  const get = mountLiveClient({
    searchLegalEntities: async (term) => {
      if (term === "antiga") return slowSearch.promise;
      if (term === "nova") return [PJ, newer];
      if (term === "erro") throw new Error("busca indisponível");
      return [];
    },
    getLegalEntity: async (id) => id === PJ.id ? slowDetail.promise : detail(newer),
  });
  const search = get("live-le-search");
  const select = get("live-le-select");
  search.value = "antiga";
  const first = search.input();
  search.value = "nova";
  const second = search.input();
  await new Promise(setImmediate);
  assert.equal(select.value, PJ.id);
  select.value = newer.id;
  await select.change();
  slowSearch.resolve([]);
  slowDetail.resolve(detail(PJ));
  await Promise.all([first, second]);
  assert.equal(select.value, newer.id);
  assert.match(get("live-le-detail").children[0].textContent, /Segunda PJ/);

  search.value = "erro";
  await search.input();
  assert.equal(get("live-le-empty").hidden, true);
  assert.match(get("feedback").textContent, /busca indisponível/);
  search.value = "sem correspondência";
  await search.input();
  assert.equal(get("live-le-empty").hidden, false);
  assert.equal(get("feedback").textContent, "");
});

test("client-live bloqueia clique repetido em PJ e mantém a seleção de representante PF", async () => {
  const creation = deferred();
  const representative = deferred();
  const links = [];
  let writes = 0;
  const get = mountLiveClient({
    createLegalEntity: async () => { writes++; return creation.promise; },
    getLegalEntity: async () => detail(PJ),
    searchParties: async () => [{ id: "pf1", name: "Pessoa física sintética" }],
    addRepresentative: async (id, payload) => { links.push({ id, ...payload }); return representative.promise; },
  });
  get("live-le-name").value = PJ.legalName;
  get("live-le-cnpj").value = PJ.cnpj;
  const create = get("live-le-create");
  const pendingCreate = create.click();
  await create.click();
  assert.equal(writes, 1);
  creation.resolve(PJ);
  await pendingCreate;
  assert.equal(create.disabled, false);

  await get("live-search").input();
  const link = get("live-le-rep-add");
  const pendingLink = link.click();
  await link.click();
  assert.deepEqual(links, [{ id: PJ.id, personId: "pf1", role: "legal_representative" }]);
  representative.resolve();
  await pendingLink;
  assert.equal(link.disabled, false);
});


test("client-live leva representante legal da PJ ao atendimento", async () => {
  const location = {};
  const get = mountLiveClient({
    searchLegalEntities: async () => [PJ],
    getLegalEntity: async () => ({
      ...detail(PJ),
      representatives: [{ personId: "pf1", name: "Pessoa Representante", role: "legal_representative" }],
    }),
  }, { attendance: true, location });
  const search = get("live-le-search");
  search.value = PJ.legalName;
  await search.input();
  get("live-le-detail").children.at(-1).children[0].click();
  assert.equal(get("live-att-person").value, "pf1");
  assert.equal(location.hash, "atendimento");
});

const dayQuery = { date: "2026-11-26", timeZone: "America/Fortaleza" };
const privateConfig = { accessToken: "teste", agendaDate: dayQuery.date, agendaTimeZone: dayQuery.timeZone };
const schedule = {
  id: "agenda-1", kind: "wake", status: "scheduled", time_zone: "America/Manaus",
  starts_at: "2026-11-26T03:30:00Z", ends_at: "2026-11-26T04:30:00Z",
};
const confirmation = { kind: "schedule_confirmation", schedule_id: schedule.id };
const agendaPage = (items = [], pending = []) => ({
  window: { date: dayQuery.date, time_zone: dayQuery.timeZone }, items, pending,
});
const title = {
  id: "<img src=x onerror=alert(1)>", status: "partially_paid",
  direction: "payable", due_date: "2026-11-26", amount_cents: 123456,
};

test("leituras usam exclusivamente /v1, Bearer, sem cache ou cabeçalhos dev", async () => {
  const fetch = captureFetch([mockResponse(200, agendaPage()), mockResponse(200, { items: [] })]);
  const api = createClientAPI({ accessToken: "t1", devTenantId: "dev-t", devCompanyId: "dev-c", fetch });
  assert.deepEqual(await api.listAgenda(dayQuery), agendaPage());
  assert.deepEqual(await api.listTitles(), { items: [], nextCursor: null });
  assert.deepEqual(fetch.calls.map(({ url }) => url), [
    "/v1/funeral/agenda?date=2026-11-26&time_zone=America%2FFortaleza",
    "/v1/finance/titles?limit=50",
  ]);
  for (const { init } of fetch.calls) {
    assert.deepEqual(init, {
      method: "GET", headers: { Accept: "application/json", Authorization: "Bearer t1" },
      cache: "no-store", redirect: "error",
    });
  }
});

test("leituras privadas sem Bearer falham antes de chamar a rede", async () => {
  const fetch = captureFetch([]);
  for (const config of [{}, { devTenantId: "dev-t" }]) {
    const api = createClientAPI({ ...config, fetch });
    await assert.rejects(api.listAgenda(dayQuery), { status: 401, code: "AUTHENTICATION_REQUIRED" });
    await assert.rejects(api.listTitles(), { status: 401, code: "AUTHENTICATION_REQUIRED" });
  }
  assert.equal(fetch.calls.length, 0);
});

test("agenda e títulos distinguem resposta malformada de lista vazia", async () => {
  const fetch = captureFetch([
    mockResponse(200, { ...agendaPage(), pending: null }),
    mockResponse(200, { items: [{ ...title, amount_cents: "123" }] }),
    mockResponse(200, { items: [], next_cursor: {} }),
  ]);
  const api = createClientAPI({ accessToken: "t1", fetch });
  await assert.rejects(api.listAgenda(dayQuery), /Resposta de leitura inválida/);
  await assert.rejects(api.listTitles(), /Resposta de leitura inválida/);
  await assert.rejects(api.listTitles(), /Resposta de leitura inválida/);
});

function dashboard(fetch, config = privateConfig, hash = "") {
  return mountLiveClient({}, { dashboard: true, config, fetch, location: { hash } });
}

const settled = () => new Promise(setImmediate);
const submit = (get) => get("private-title-filters").submit({ preventDefault() {} });

test("prévia sem configuração preserva agenda e fila sintéticas sem chamar a rede", async () => {
  const fetch = captureFetch([]);
  const { get, navigate } = mountLiveClient({}, { dashboard: true, fetch });
  assert.equal(get("private-day").hidden, true);
  assert.equal(get("demo-day").hidden, false);
  await navigate("#financeiro");
  assert.equal(get("private-finance").hidden, true);
  assert.equal(get("demo-finance").hidden, false);
  await navigate("#atendimento");
  get("queue").click();
  await navigate("#sincronizacao");
  get("connect").click();
  get("sync").click();
  assert.match(get("operations").children[0].textContent, /Sincronizada na simulação/);
  assert.equal(fetch.calls.length, 0);
});

test("modo privado sem Bearer esconde demonstração e declara leitura indisponível", async () => {
  const fetch = captureFetch([]);
  for (const config of [{}, { devTenantId: "dev-t", devCompanyId: "dev-c" }]) {
    const { get, navigate } = dashboard(fetch, config);
    assert.equal(get("demo-day").hidden, true);
    assert.equal(get("private-day").hidden, false);
    assert.match(get("private-day-status").textContent, /sem sessão Bearer/);
    await navigate("#financeiro");
    assert.equal(get("demo-finance").hidden, true);
    assert.equal(get("private-title-filters").hidden, true);
    assert.match(get("private-title-result-status").textContent, /sem sessão Bearer/);
  }
  assert.equal(fetch.calls.length, 0);
});

test("agenda exige data civil válida e fuso IANA explícitos antes da consulta", () => {
  const fetch = captureFetch([]);
  for (const [config, message] of [
    [{ accessToken: "t1" }, /Configure a data civil e o fuso IANA/],
    [{ ...privateConfig, agendaDate: "2026-02-30" }, /data civil da agenda é inválida/],
    [{ ...privateConfig, agendaTimeZone: "+03:00" }, /fuso da agenda é inválido/],
    [{ ...privateConfig, agendaTimeZone: "America/Inexistente" }, /fuso da agenda é inválido/],
  ]) {
    const { get } = dashboard(fetch, config);
    assert.equal(get("demo-day").hidden, true);
    assert.equal(get("private-day-refresh").hidden, true);
    assert.match(get("private-day-status").textContent, message);
  }
  assert.equal(fetch.calls.length, 0);
});

test("painel exibe data e horários no fuso consultado e confirmações vindas da API", async () => {
  const loading = deferred();
  const fetch = captureFetch([loading.promise, mockResponse(200, agendaPage([schedule])), mockResponse(200, agendaPage())]);
  const { get } = dashboard(fetch);
  assert.equal(get("demo-day").hidden, true);
  assert.match(get("private-day-status").textContent, /Carregando agenda/);
  assert.equal(get("private-day-results").attributes["aria-busy"], "true");
  assert.equal(get("private-day-window").textContent, "26/11/2026 · America/Fortaleza");
  loading.resolve(mockResponse(200, agendaPage([schedule], [confirmation])));
  await settled();
  const item = get("private-day-items").children[0];
  assert.equal(item.children[0].textContent, "Velório");
  assert.equal(item.children[2], "Agendado");
  assert.match(item.children[4].textContent, /00:30 GMT-3/);
  assert.equal(item.children[4].dateTime, schedule.starts_at);
  assert.match(get("private-day-pending").children[0].children.at(-1).textContent, /agenda-1/);
  assert.equal(get("private-day-pending-empty").hidden, true);
  assert.equal(get("private-day-results").attributes["aria-busy"], "false");
  await get("private-day-refresh").click();
  assert.equal(get("private-day-items").children.length, 1);
  assert.equal(get("private-day-pending").children.length, 0);
  assert.equal(get("private-day-pending-empty").hidden, false);
  await get("private-day-refresh").click();
  assert.equal(get("private-day-results").hidden, true);
  assert.match(get("private-day-status").textContent, /Nenhum agendamento/);
});

test("financeiro aplica filtros inclusivos, preserva cursor opaco e formata int64 sem arredondar", async () => {
  const nextCursor = "página+2/?=";
  const firstPage = { items: [title, { ...title, id: "legado", amount_cents: 0 }], next_cursor: nextCursor };
  const secondPage = {
    ...mockResponse(200),
    text: async () => JSON.stringify({ items: [{ ...title, id: "t2", amount_cents: 1 }] })
      .replace('"amount_cents":1', '"amount_cents":9223372036854775807'),
  };
  const fetch = captureFetch([mockResponse(200, { items: [] }), mockResponse(200, firstPage), secondPage]);
  const { get } = dashboard(fetch, privateConfig, "#financeiro");
  await settled();
  assert.match(get("private-title-result-status").textContent, /Nenhum título/);
  assert.equal(get("private-title-next").hidden, true);
  for (const [key, value] of [
    ["status", "partially_paid"], ["direction", "payable"], ["due-from", "2026-11-26"], ["due-to", "2026-11-26"],
  ]) get("private-title-" + key).value = value;
  await submit(get);
  assert.equal(get("private-title-results").hidden, false);
  assert.equal(get("private-title-next").disabled, false);
  const row = get("private-title-rows").children[0].children;
  assert.equal(row[0].textContent, title.id);
  assert.equal(row[0].children.length, 0);
  assert.equal(row[1].children[0].textContent, "26/11/2026");
  assert.equal(row[2].textContent, "A pagar");
  assert.match(row[3].textContent, /^R\$\s1\.234,56$/);
  assert.equal(row[4].textContent, "Parcialmente pago");
  assert.match(get("private-title-rows").children[1].children[3].textContent, /^R\$\s0,00$/);
  await get("private-title-next").click();
  const pageQuery = (call) => Object.fromEntries(new URL(call.url, "https://loja.test").searchParams);
  const filters = {
    status: "partially_paid", direction: "payable", due_from: "2026-11-26", due_to: "2026-11-26", limit: "50",
  };
  assert.deepEqual(pageQuery(fetch.calls[1]), filters);
  assert.deepEqual(pageQuery(fetch.calls[2]), { ...filters, cursor: nextCursor });
  assert.match(get("private-title-rows").children[0].children[3].textContent, /^R\$\s92\.233\.720\.368\.547\.758,07$/);
  assert.equal(get("private-title-next").hidden, true);
  get("private-title-due-to").value = "2026-11-25";
  await submit(get);
  assert.equal(fetch.calls.length, 3);
  assert.match(get("private-title-result-status").textContent, /intervalo de vencimento válido/);
});

test("painel e financeiro exibem erros sem mostrar registros sintéticos ou respostas da API", async () => {
  const cases = [
    [new Error("queda de rede"), /Sem conexão/],
    [mockResponse(401, { code: "AUTHENTICATION_REQUIRED" }), /Sessão ausente ou expirada/],
    [mockResponse(403, { code: "PERMISSION_DENIED" }), /não tem permissão/],
    [mockResponse(503, { code: "AUTHENTICATION_UNAVAILABLE" }), /temporariamente indisponível/],
    [mockResponse(500, { code: "INTERNAL", message: "detalhe privado" }), /Não foi possível concluir/],
  ];
  for (const [hash, statusId, resultsId] of [
    ["", "private-day-status", "private-day-results"],
    ["#financeiro", "private-title-result-status", "private-title-results"],
  ]) {
    for (const [response, message] of cases) {
      const { get } = dashboard(captureFetch([response]), privateConfig, hash);
      await settled();
      assert.match(get(statusId).textContent, message);
      assert.doesNotMatch(get(statusId).textContent, /detalhe privado/);
      assert.equal(get(resultsId).hidden, true);
    }
  }
  const blocked = mockResponse(403, { code: "ENTITLEMENT_MODULE_NOT_LICENSED" });
  const { get } = dashboard(captureFetch([blocked]));
  await settled();
  assert.match(get("private-day-status").textContent, /Agenda bloqueada.*licença Funerária/);
});

test("financeiro ignora página antiga após aplicar novos filtros", async () => {
  const loading = deferred();
  const fetch = captureFetch([
    mockResponse(200, { items: [title], next_cursor: "cursor-antigo" }),
    loading.promise,
    mockResponse(200, { items: [{ ...title, id: "título novo" }], next_cursor: "cursor-novo" }),
  ]);
  const { get } = dashboard(fetch, privateConfig, "#financeiro");
  await settled();
  const next = get("private-title-next");
  const previous = next.click();
  assert.equal(next.disabled, true);
  assert.match(get("private-title-result-status").textContent, /Carregando títulos/);
  get("private-title-status").value = "paid";
  await submit(get);
  loading.resolve(mockResponse(401, {}));
  await previous;
  assert.equal(get("private-title-rows").children[0].children[0].textContent, "título novo");
  assert.equal(next.disabled, false);
  assert.doesNotMatch(get("private-title-result-status").textContent, /Sessão/);
  assert.match(fetch.calls[1].url, /cursor=cursor-antigo/);
  assert.doesNotMatch(fetch.calls[2].url, /cursor=/);
});

test("agenda ignora resposta da visita anterior depois de navegar e voltar", async () => {
  const loading = deferred();
  const fetch = captureFetch([loading.promise, mockResponse(200, { items: [] }), mockResponse(200, agendaPage())]);
  const { get, navigate } = dashboard(fetch);
  await navigate("#financeiro");
  await navigate("#painel");
  loading.resolve(mockResponse(200, agendaPage([schedule], [confirmation])));
  await settled();
  assert.equal(get("private-day-items").children.length, 0);
  assert.match(get("private-day-status").textContent, /Nenhum agendamento/);
});
