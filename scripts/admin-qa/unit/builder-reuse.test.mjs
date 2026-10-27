// Reuse helpers: ops apply and undo in one step, labels, filters, guards.
// Run: node --test scripts/admin-qa/unit/builder-reuse.test.mjs
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
const R = await import(new URL("../../../backend/web/admin/js/builder/reuseops.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

const kinds = [
  { kind: "container", children: true },
  { kind: "text" },
  { kind: "slot" },
  { kind: "ref" },
];
S.setChildKinds(kinds);
const BLK = "11111111-1111-4111-8111-111111111111";
const doc = () => ({
  v: 2,
  nodes: [
    { id: "a", t: "container", c: [{ id: "a1", t: "text" }, { id: "a2", t: "container", c: [{ id: "a2x", t: "text" }] }, { id: "a3", t: "text" }] },
    { id: "sec", t: "container", p: { tag: "section" }, c: [{ id: "comp", t: "component", p: { type: "hero" } }] },
    { id: "s", t: "container", c: [{ id: "sl", t: "slot" }] },
    { id: "r", t: "ref", p: { block: BLK } },
  ],
});
const ids = (ns) => ns.map((n) => n.id + (n.c ? `(${ids(n.c)})` : "")).join(",");
const fresh = (...a) => a.map((id) => ({ id, t: "text" }));

function roundTrip(d, ops) {
  const st = S.commit(S.createStore(d), ops);
  const back = S.undo(st);
  assert.deepEqual(back.doc, d, "one undo restores the tree");
  return st.doc;
}

test("refNode", () => {
  assert.deepEqual(R.refNode(BLK, "x"), { id: "x", t: "ref", p: { block: BLK } });
  assert.match(R.refNode(BLK).id, /^n[0-9a-f]{10}$/);
});

test("makeBlockOps in place: middle, last child, depth, v1 section", () => {
  const d = doc();
  let out = roundTrip(d, R.makeBlockOps(d, "a2", BLK));
  assert.equal(ids(out.nodes).startsWith("a(a1,n"), true);
  assert.equal(S.locate(out, "a2"), null);
  const ops = R.makeBlockOps(d, "a3", BLK);
  out = roundTrip(d, ops);
  const a = out.nodes[0].c;
  assert.deepEqual(a.map((n) => n.t), ["text", "container", "ref"]);
  out = roundTrip(d, R.makeBlockOps(d, "a2x", BLK));
  assert.equal(S.find(out, "a").c[1].c[0].t, "ref");
  out = roundTrip(d, R.makeBlockOps(d, "sec", BLK));
  assert.equal(out.nodes[1].t, "ref");
  assert.throws(() => R.makeBlockOps(d, "nope", BLK));
});

test("detachOps keeps order and place", () => {
  const d = doc();
  const out = roundTrip(d, R.detachOps(d, "r", fresh("x", "y", "z")));
  assert.deepEqual(out.nodes.slice(3).map((n) => n.id), ["x", "y", "z"]);
  const nested = S.commit(S.createStore(d), R.makeBlockOps(d, "a2", BLK)).doc;
  const rid = S.find(nested, "a").c[1].id;
  const out2 = roundTrip(nested, R.detachOps(nested, rid, fresh("p", "q", "w")));
  assert.deepEqual(S.find(out2, "a").c.map((n) => n.id), ["a1", "p", "q", "w", "a3"]);
});

test("insertOps uses the palette insert point", () => {
  const d = doc();
  let out = roundTrip(d, R.insertOps(d, "a", fresh("x", "y"), kinds));
  assert.deepEqual(S.find(out, "a").c.map((n) => n.id).slice(-2), ["x", "y"]);
  out = roundTrip(d, R.insertOps(d, "a1", fresh("x", "y"), kinds));
  assert.deepEqual(S.find(out, "a").c.map((n) => n.id).slice(0, 3), ["a1", "x", "y"]);
  out = roundTrip(d, R.insertOps(d, "", fresh("x"), kinds));
  assert.equal(out.nodes.at(-1).id, "x");
  out = roundTrip(d, R.insertOps(d, "sec", fresh("x"), kinds));
  assert.equal(out.nodes[2].id, "x", "after a v1 section, not inside");
});

test("reuseItems lists templates then blocks", () => {
  const reuse = { templates: [{ id: "t1", name: "Pricing row", hint: "3 cols" }], blocks: [{ id: "b1", name: "Footer CTA" }] };
  const all = R.reuseItems(reuse, "");
  assert.deepEqual(all.map((i) => [i.key, i.group]), [["tpl:t1", "Patterns"], ["blk:b1", "Blocks"]]);
  assert.deepEqual(R.reuseItems(undefined), []);
});

test("refLabel", () => {
  const blocks = [{ id: BLK, name: "Footer CTA" }];
  assert.equal(R.refLabel({ p: { block: BLK } }, blocks), "Block: Footer CTA");
  assert.equal(R.refLabel({ p: { block: "zz" } }, blocks), "Block: (missing)");
  assert.equal(R.refLabel({ p: { block: BLK } }, undefined), "Block: (missing)");
});

test("canMakeBlock", () => {
  const d = doc();
  assert.equal(R.canMakeBlock(d, "a", kinds), true);
  assert.equal(R.canMakeBlock(d, "a2x", kinds), true);
  assert.equal(R.canMakeBlock(d, "sec", kinds), true);
  assert.equal(R.canMakeBlock(d, "comp", kinds), false, "child of a v1 section");
  assert.equal(R.canMakeBlock(d, "r", kinds), false, "a ref");
  assert.equal(R.canMakeBlock(d, "s", kinds), false, "contains a slot");
  assert.equal(R.canMakeBlock(d, "", kinds), false, "page root");
  assert.equal(R.canMakeBlock(d, "nope", kinds), false);
});

test("templateSource", () => {
  const d = doc();
  assert.deepEqual(R.templateSource(d, "a"), { nodeId: "a" });
  assert.deepEqual(R.templateSource(d, ""), { page: true });
  assert.deepEqual(R.templateSource(d, "gone"), { page: true });
});

test("a ref reads as Block: <name> everywhere, and the reuse chunk speaks of blocks and templates only", async () => {
  assert.equal(R.refLabel({ p: { block: "b1" } }, [{ id: "b1", name: "Promo" }]), "Block: Promo");
  assert.equal(R.refLabel({ p: { block: "zz" } }, []), "Block: (missing)");
  const { readFile } = await import("node:fs/promises");
  for (const f of ["reuse.js", "savedialog.js", "ctxmenu.js"]) {
    const src = await readFile(new URL(`../../../backend/web/admin/js/builder/${f}`, import.meta.url), "utf8");
    assert.ok(!/Make synced block|Synced block|synced block|Save selection as template/.test(src.replace(/\/\/.*$/gm, "")), f);
  }
});
