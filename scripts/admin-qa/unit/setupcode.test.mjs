// Setup page: the code field is prefilled from #code=X and the fragment is then dropped.
// Run: node --test scripts/admin-qa/unit/setupcode.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/setupcode.js", import.meta.url), "utf8");
let n = 0;
// A data: URL with a unique tail is a fresh module each time, so the script runs again.
const load = () => import(`data:text/javascript,${encodeURIComponent(src)}%0A//${++n}`);

function setup(hash, el) {
  const calls = [];
  globalThis.location = { hash, pathname: "/admin/setup", search: "?x=1" };
  globalThis.document = { getElementById: (id) => (id === "code" ? el : null) };
  globalThis.history = { replaceState: (...a) => calls.push(a) };
  return calls;
}

test("sets the value and strips the fragment", async () => {
  const el = { value: "", disabled: false };
  const calls = setup("#code=ABCD-EFGH-JKMN", el);
  await load();
  assert.equal(el.value, "ABCD-EFGH-JKMN");
  assert.deepEqual(calls, [[null, "", "/admin/setup?x=1"]]);
});

test("no hash is a no-op", async () => {
  const el = { value: "", disabled: false };
  const calls = setup("", el);
  await load();
  assert.equal(el.value, "");
  assert.equal(calls.length, 0);
});

test("malformed escape leaves the field empty but strips the fragment", async () => {
  const el = { value: "", disabled: false };
  const calls = setup("#code=%E0%A4%A", el);
  await load();
  assert.equal(el.value, "");
  assert.equal(calls.length, 1);
});

test("a disabled input is untouched", async () => {
  const el = { value: "", disabled: true };
  setup("#code=ABCD", el);
  await load();
  assert.equal(el.value, "");
});

test("an existing value is kept", async () => {
  const el = { value: "KEEP", disabled: false };
  setup("#code=ABCD", el);
  await load();
  assert.equal(el.value, "KEEP");
});
