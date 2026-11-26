import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createPanelDemo } from "../../staging/panel-demo.js";

function readSource(path) {
  return readFileSync(new URL(path, import.meta.url), "utf8");
}

function assertTouchTarget(styles, selector) {
  const rule = styles.split(`${selector} {`)[1]?.split("}")[0];
  assert.ok(rule, `Missing touch target rule for ${selector}`);
  for (const dimension of ["width", "height"]) {
    const pixels = rule.match(new RegExp(`min-${dimension}:\\s*(\\d+)px\\s*;`))?.[1];
    assert.ok(Number(pixels) >= 44, `${selector} must have a minimum ${dimension} of 44px`);
  }
}

test("staging confirmation button has a 44px minimum touch target", () => {
  const markup = readSource("../../staging/index.html");
  assert.match(markup, /<link rel="stylesheet" href="\/staging\.css">/);
  assert.match(markup, /<button[^>]+id="queue"/);
  assertTouchTarget(readSource("../../staging/staging.css"), "#queue");
});

test("control-plane license action buttons share a 44px minimum touch target", () => {
  const markup = readSource("../app.js");
  const actions = markup.split('<div class="detail-actions">')[1]?.split("</div>")[0];
  assert.ok(actions, "Missing license actions");
  assert.deepEqual(
    [...actions.matchAll(/<button[^>]+data-action="([^"]+)"/g)].map((match) => match[1]),
    ["grant", "revoke", "quota", "validity"],
  );
  assertTouchTarget(readSource("../styles.css"), ".detail-actions button[data-action]");
});

test("staging uses isolated fixtures with versioning and idempotency", async () => {
  const path = "/v1/companies/empresa-demo/entitlements/funeral";
  const versions = [];
  const demo = createPanelDemo((license) => versions.push(license.version));
  const data = { method: "PATCH", headers: { "Idempotency-Key": "quota-1" }, body: '{"quota":8,"version":1}' };
  const saved = await demo(path, data).json();
  assert.equal(saved.quota, 8);
  assert.equal(saved.version, 2);
  assert.deepEqual(await demo(path, data).json(), saved);
  assert.equal((await demo(path, { ...data, body: '{"quota":7,"version":1}' }).json()).code, "IDEMPOTENCY_CONFLICT");
  assert.equal((await demo(path, { ...data, headers: { "Idempotency-Key": "quota-2" } }).json()).code, "STALE_VERSION");
  assert.equal((await demo(`${path}/audit`, { method: "GET" }).json()).items.length, 1);
  assert.deepEqual(versions, [1, 2]);
  assert.equal(demo(path.replace("empresa-demo", "other-company"), { method: "GET" }).status, 404);
  assert.equal((await createPanelDemo()(path, { method: "GET" }).json()).quota, 5);
});


test("manager.amparius.com serves the SaaS panel at the root", () => {
  const caddy = readSource("../../../deploy/staging/Caddyfile");
  const block = caddy.split("manager.amparius.com {")[1]?.split("\n}")[0];
  assert.ok(block, "Missing manager.amparius.com site block");
  assert.ok(block.includes("root * /srv/amparius-preview/manager"));
  assert.ok(block.includes("path / /staging.css /panel-demo.js /styles.css /helpers.js /app.js"));
});

test("preview package emits a manager tree with root-relative branding", () => {
  const script = readSource("../../../deploy/staging/prepare-preview.sh");
  assert.ok(script.includes("$destination/manager/index.html"));
  assert.ok(script.includes("./assets/brand/amparius-logo.svg"));
});
test("web client has no pointer to the SaaS panel", () => {
  const markup = readSource("../../staging/index.html");
  assert.ok(!markup.includes("/painel/"), "web client must not link to /painel/");
  assert.ok(!markup.includes("manager.amparius.com"), "web client must not reference the manager host");
  assert.ok(!markup.includes("Abrir painel SaaS"), "web client must not advertise the SaaS panel");
});

test("staging package keeps the surfaces separate", () => {
  const caddy = readSource("../../../deploy/staging/Caddyfile");
  const hml = caddy.split("hml.amparius.com {")[1]?.split("\n}")[0];
  assert.ok(hml, "Missing hml.amparius.com site block");
  assert.ok(!hml.includes("/painel/"), "hml must not serve the admin panel");
  assert.ok(hml.includes("/styles.css"), "hml must serve the shared stylesheet");
  const script = readSource("../../../deploy/staging/prepare-preview.sh");
  assert.ok(!script.includes("$destination/painel"), "package must not emit the panel under /painel/");
  assert.ok(script.includes("$destination/manager/index.html"));
});
test("store preview has one destination for each menu and starts on the day panel", () => {
  const markup = readSource("../../staging/index.html");
  const nav = markup.split('<nav aria-label="Operação da loja">')[1].split("</nav>")[0];
  const destinations = [...nav.matchAll(/href="#([^"]+)"/g)].map((match) => match[1]);
  assert.deepEqual(destinations, [
    "painel", "atendimento", "contratos", "cadastros", "cemiterio",
    "flores", "financeiro", "fiscal", "relatorios", "sincronizacao",
  ]);
  const screens = [...markup.matchAll(/data-screen="([^"]+)"([^>]*)>/g)];
  assert.equal(screens.length, destinations.length);
  assert.deepEqual(screens.map(([, screen]) => screen).sort(), [...destinations].sort());
  assert.deepEqual(screens.filter(([, , attrs]) => !/\bhidden\b/.test(attrs)).map(([, screen]) => screen), ["painel"]);
  assert.match(nav, /href="#painel" aria-current="page"/);
});
