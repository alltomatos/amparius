import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { createClientAPI } = require("./client-api.js");

function mockResponse(status, payload) {
  return { ok: status >= 200 && status < 300, status, json: async () => payload };
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
