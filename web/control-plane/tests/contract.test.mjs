import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const H = require("../helpers.js");

test("escapeHtml escapes special chars", () => {
  assert.equal(H.escapeHtml('<script>"x"&\'y\'</script>'), "&lt;script&gt;&quot;x&quot;&amp;&#039;y&#039;&lt;/script&gt;");
});

test("maskCnpj masks all but last 4", () => {
  assert.equal(H.maskCnpj("12345678000199"), "**********0199");
  assert.equal(H.maskCnpj(null), "mascarado");
});

test("displayDate returns pt-BR medium", () => {
  assert.match(H.displayDate("2026-11-25T12:00:00Z"), /25 de nov/);
  assert.equal(H.displayDate(null), "sem validade final");
});

test("normalizeError maps payload", () => {
  const e = H.normalizeError({ code: "TENANT_MISMATCH", message: "fora", traceId: "t1", details: { x: 1 } }, 403);
  assert.equal(e.code, "TENANT_MISMATCH");
  assert.equal(e.traceId, "t1");
});

test("normalizeError fallbacks on empty", () => {
  const e = H.normalizeError({}, 500);
  assert.equal(e.code, "HTTP_500");
});

test("pageState", () => {
  assert.equal(H.pageState([], false, null), "empty");
  assert.equal(H.pageState([{ id: 1 }], false, null), "success");
  assert.equal(H.pageState([], true, null), "loading");
  assert.equal(H.pageState([], false, new Error("x")), "error");
});