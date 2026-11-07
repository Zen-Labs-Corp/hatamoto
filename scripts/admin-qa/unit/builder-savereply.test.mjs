// The save reply mapping and its re-exports. Run: node --test scripts/admin-qa/unit/builder-savereply.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const R = await import(new URL("../../../backend/web/admin/js/builder/savereply.js", import.meta.url));

test("a 2xx with ok maps to ok and the new version", () => {
  const r = R.mapSaveReply(200, { ok: true, version: "v2" });
  assert.equal(r.ok, true);
  assert.equal(r.version, "v2");
});

test("409 with conflict maps to a conflict carrying version and message", () => {
  const r = R.mapSaveReply(409, { conflict: true, version: "v3", error: "Changed elsewhere" });
  assert.deepEqual(r, { conflict: true, version: "v3", message: "Changed elsewhere" });
});

test("422 stops saving with the server message; the body may be empty", () => {
  const r = R.mapSaveReply(422, { error: "Name is required", code: "invalid" });
  assert.equal(r.stop, true);
  assert.equal(r.message, "Name is required");
  assert.notEqual(R.mapSaveReply(422).ok, true);
  assert.equal(R.mapSaveReply(403).stop, true);
  assert.deepEqual(R.mapSaveReply(500, {}), { ok: false, message: "The page could not be saved." });
});

test("the old import paths still export the moved functions", async () => {
  const sync = await import(new URL("../../../backend/web/admin/js/builder/sync.js", import.meta.url));
  assert.equal(sync.mapSaveReply, R.mapSaveReply);
  const drag = await import(new URL("../../../backend/web/admin/js/builder/drag.js", import.meta.url));
  const src = await import(new URL("../../../backend/web/admin/js/builder/dragsource.js", import.meta.url));
  assert.equal(drag.dragSource, src.dragSource);
  const keys = await import(new URL("../../../backend/web/admin/js/builder/keys.js", import.meta.url));
  assert.equal(typeof keys.createSheet, "function");
  assert.equal(keys.createSheet.length, 0); // groups defaults to GROUPS
});
