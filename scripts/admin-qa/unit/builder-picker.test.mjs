// The "+" picker's layouts: the same structures that Go validates (backend/internal/builder/picker_layouts_test.go
// reads testdata/picker_layouts.json). Run: node --test scripts/admin-qa/unit/builder-picker.test.mjs
//   regenerate the fixture: HM_PICKER_UPDATE=1 node --test scripts/admin-qa/unit/builder-picker.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { LAYOUTS, MORE } = await import(new URL("../../../backend/web/admin/js/builder/picker.js", import.meta.url));
const FIXTURE = new URL("../../../backend/internal/builder/testdata/picker_layouts.json", import.meta.url);

const bare = (n) => {
  const { id, c, ...rest } = n;
  assert.ok(id, "every node has an id");
  return { ...rest, ...(c ? { c: c.map(bare) } : {}) };
};
const idsOf = (n) => [n.id, ...(n.c ?? []).flatMap(idsOf)];

test("layouts are named, unique, and each make() gives fresh ids", () => {
  assert.deepEqual(LAYOUTS.map((l) => l.id), ["1", "2", "3", "4", "1-2", "2-1", "2x2"]);
  assert.equal(new Set(LAYOUTS.map((l) => l.name)).size, LAYOUTS.length);
  const a = LAYOUTS.flatMap((l) => idsOf(l.make()));
  const b = LAYOUTS.flatMap((l) => idsOf(l.make()));
  assert.equal(new Set([...a, ...b]).size, a.length + b.length);
});

test("the layouts' shapes: columns widget for equal splits, a grid container for the rest", () => {
  const by = Object.fromEntries(LAYOUTS.map((l) => [l.id, l.make()]));
  assert.equal(by["1"].t, "container");
  for (const n of [2, 3, 4]) {
    assert.equal(by[n].t, "columns");
    assert.equal(by[n].p.count, n);
    assert.equal(by[n].c.length, n);
  }
  assert.equal(by["1-2"].p.layout, "grid");
  assert.equal(by["1-2"].s.base.gtc, "1fr 2fr");
  assert.equal(by["2-1"].s.base.gtc, "2fr 1fr");
  assert.equal(by["2x2"].s.base.cols, "2");
  assert.equal(by["2x2"].c.length, 4);
  for (const l of LAYOUTS) assert.equal(l.n, (l.make().c ?? [1]).length, l.id);
});

test("the Go fixture lists exactly these layouts", () => {
  const now = LAYOUTS.map((l) => ({ id: l.id, node: bare(l.make()) }));
  if (process.env.HM_PICKER_UPDATE) writeFileSync(FIXTURE, JSON.stringify(now, null, 2) + "\n");
  assert.deepEqual(JSON.parse(readFileSync(FIXTURE, "utf8")), now);
});

test("the rows under the layouts leave for the Add tab: an element, or a ready-made section", () => {
  assert.deepEqual(MORE.map((r) => r.mode), ["element", "pattern"]);
  assert.deepEqual(MORE.map((r) => r.label), ["Choose an element…", "Insert a ready-made section…"]);
});
