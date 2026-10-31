// What dragging a Style label starts from: a token resolves, auto and custom values do not scrub, the unit is read per drag.
// Run: node --test scripts/admin-qa/unit/builder-scrubof.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);

const { scrubOf } = await import(new URL("builder/style.js", root));
const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));
const entry = (k) => table.find((e) => e.key === k);
const palette = { space: { 6: "1.5rem" }, fs: { "2xl": "2rem" } };
const make = (key, value, pal = palette) => {
  const v = { value };
  const writes = [];
  const s = scrubOf(entry(key), () => v.value, (x) => writes.push(x), pal);
  return { s, v, writes };
};

test("a font size token starts from what it resolves to, not from 0", () => {
  const { s } = make("fs", "2xl");
  assert.equal(s.get(), 2);
  assert.equal(s.step(), 0.1, "rem step");
});

test("a space token resolves through the palette; a line-height token through the table", () => {
  assert.equal(make("gap", "space-6").s.get(), 1.5);
  assert.equal(make("lh", "normal").s.get(), 1.5);
});

test("auto, an unresolvable token and a custom value do not scrub", () => {
  assert.equal(make("w", "auto").s.get(), null);
  assert.equal(make("ls", "normal").s.get(), null, "a token that is not a number");
  assert.equal(make("gap", "space-6", null).s.get(), null, "no palette entry for the step");
});

test("an empty value starts at 0 in px; unit, step and bounds follow the value at each drag start", () => {
  const { s, v, writes } = make("w", "");
  assert.equal(s.get(), 0);
  s.apply(5, {});
  assert.deepEqual(writes, ["5px"]);
  v.value = "2rem";
  assert.equal(s.get(), 2);
  assert.equal(s.step(), 0.1, "after typing rem the step is a rem step");
  s.apply(2.5, {});
  assert.equal(writes.at(-1), "2.5rem");
  assert.ok(s.bounds().max < 20, "rem bounds, not px bounds");
});
