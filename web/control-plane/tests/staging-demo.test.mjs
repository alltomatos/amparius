import { test } from "node:test";
import assert from "node:assert/strict";
import { createPanelDemo } from "../../staging/panel-demo.js";

test("staging uses isolated fixtures with versioning and idempotency", async () => {
  const path = "/v1/companies/empresa-demo/entitlements/funeral";
  const demo = createPanelDemo();
  const data = { method: "PATCH", headers: { "Idempotency-Key": "quota-1" }, body: '{"quota":8,"version":1}' };
  const saved = await demo(path, data).json();
  assert.equal(saved.quota, 8);
  assert.equal(saved.version, 2);
  assert.deepEqual(await demo(path, data).json(), saved);
  assert.equal((await demo(path, { ...data, body: '{"quota":7,"version":1}' }).json()).code, "IDEMPOTENCY_CONFLICT");
  assert.equal((await demo(path, { ...data, headers: { "Idempotency-Key": "quota-2" } }).json()).code, "STALE_VERSION");
  assert.equal((await demo(`${path}/audit`, { method: "GET" }).json()).items.length, 1);
  assert.equal(demo(path.replace("empresa-demo", "other-company"), { method: "GET" }).status, 404);
  assert.equal((await createPanelDemo()(path, { method: "GET" }).json()).quota, 5);
});
