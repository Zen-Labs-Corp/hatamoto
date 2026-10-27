// The Add tab's Recent row storage: per user, deduped, capped, and safe when storage throws.
// Run: node --test scripts/admin-qa/unit/builder-recent.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const R = await import(new URL("../../../backend/web/admin/js/builder/recent.js", import.meta.url));
const store = (init = {}) => ({ data: { ...init }, getItem(k) { return this.data[k] ?? null; }, setItem(k, v) { this.data[k] = v; } });

test("push puts the key first, once, and keeps at most 8", () => {
  assert.deepEqual(R.push(["a", "b"], "b"), ["b", "a"]);
  const many = R.push(["1", "2", "3", "4", "5", "6", "7", "8"], "9");
  assert.equal(many.length, 8);
  assert.deepEqual([many[0], many.at(-1)], ["9", "7"]);
});

test("load and save are per user", () => {
  const s = store();
  R.save("u1", ["heading"], s);
  assert.deepEqual(R.load("u1", s), ["heading"]);
  assert.deepEqual(R.load("u2", s), []);
});

test("storage that throws or holds junk gives an empty list and never throws", () => {
  const bad = { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("full"); } };
  assert.deepEqual(R.load("u", bad), []);
  assert.doesNotThrow(() => R.save("u", ["x"], bad));
  assert.deepEqual(R.load("u", store({ "hm-builder-recent:u": "{not json" })), []);
  assert.deepEqual(R.load("u", store({ "hm-builder-recent:u": '{"a":1}' })), []);
  assert.deepEqual(R.load("u", store({ "hm-builder-recent:u": '["a",3,"b"]' })), ["a", "b"]);
  assert.deepEqual(R.load("u", undefined), []);
});

test("use re-reads storage, so two owners (Add tab and Ctrl+K) keep each other's inserts", () => {
  const s = store();
  R.use("u", "image", s); // Ctrl+K
  R.use("u", "heading", s); // the Add tab, which loaded before the first insert
  assert.deepEqual(R.load("u", s), ["heading", "image"]);
  assert.deepEqual(R.use("u", "image", s), ["image", "heading"]);
});
