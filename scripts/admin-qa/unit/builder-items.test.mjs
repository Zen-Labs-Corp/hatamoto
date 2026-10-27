// Items panel: Add item, Move up/down and Delete are the existing ops; a List edits its lines.
// Run: node --test scripts/admin-qa/unit/builder-items.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register("data:text/javascript," + encodeURIComponent(`export async function resolve(s, c, next) { if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true }; return next(s, c); }`));

class El {
  constructor(tag) { Object.assign(this, { tag, attrs: {}, kids: [], on: {}, dataset: {}, textContent: "", value: "", disabled: false, parent: null }); this.classList = { add() {}, toggle() {}, contains: () => false }; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { for (const x of k) { if (typeof x === "object") x.parent = this; this.kids.push(x); } }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t) { for (const f of this.on[t] ?? []) f({ target: this }); }
  walk(f) { f(this); this.kids.forEach((k) => typeof k === "object" && k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t) };
globalThis.window = globalThis;
const { itemsPanel, itemName, swapLines, linesOf } = await import(new URL("builder/items.js", root));

const kinds = [{ kind: "tabpanel", default: { label: "Tab" } }, { kind: "accorditem", default: { title: "Item" } }];
const setup = (node) => {
  const commits = [], said = [], picked = [];
  const api = { kinds, getDoc: () => ({ nodes: [node] }), announce: (m) => said.push(m), select: (id) => picked.push(id) };
  const el = itemsPanel(node, { api, commit: (ops, opts) => (commits.push({ ops, opts }), true), draw() {} });
  const btn = (text) => { const out = []; el.walk((n) => n.tag === "button" && (n.attrs["aria-label"] === text || n.textContent === text) && out.push(n)); return out; };
  return { el, commits, said, picked, btn };
};
const tabs = { id: "t", t: "tabs", c: [{ id: "p1", t: "tabpanel", p: { label: "One" } }, { id: "p2", t: "tabpanel", p: { label: "Two" } }] };

test("Add tab inserts the kind's default child at the end", () => {
  const m = setup(tabs);
  m.btn("Add tab")[0].fire("click");
  const { ops } = m.commits[0];
  assert.equal(ops.length, 1);
  assert.deepEqual([ops[0].op, ops[0].parent, ops[0].index, ops[0].node.t, ops[0].node.p], ["insert", "t", 2, "tabpanel", { label: "Tab" }]);
});

test("Move up/down are move ops; the first has no Move up, the last no Move down", () => {
  const m = setup(tabs);
  assert.equal(m.btn("Move tab One up")[0].disabled, true);
  assert.equal(m.btn("Move tab Two down")[0].disabled, true);
  m.btn("Move tab Two up")[0].fire("click");
  assert.deepEqual(m.commits[0].ops, [{ op: "move", id: "p2", parent: "t", index: 0 }]);
  m.btn("Move tab One down")[0].fire("click");
  assert.deepEqual(m.commits[1].ops, [{ op: "move", id: "p1", parent: "t", index: 1 }]);
});

test("Delete removes the child; the name selects it", () => {
  const m = setup(tabs);
  m.btn("Delete tab One")[0].fire("click");
  assert.deepEqual(m.commits[0].ops, [{ op: "remove", id: "p1" }]);
  m.btn("Select tab Two")[0].fire("click");
  assert.deepEqual(m.picked, ["p2"]);
});

test("a List edits its lines through setProp items, one commit per action", () => {
  const m = setup({ id: "l", t: "list", p: { items: "a\nb\nc" } });
  m.btn("Move item 3 up")[0].fire("click");
  m.btn("Delete item 1")[0].fire("click");
  m.btn("Add item")[0].fire("click");
  assert.deepEqual(m.commits.map((c) => c.ops), [
    [{ op: "setProp", id: "l", key: "items", value: "a\nc\nb" }],
    [{ op: "setProp", id: "l", key: "items", value: "b\nc" }],
    [{ op: "setProp", id: "l", key: "items", value: "a\nb\nc\n" }],
  ]);
});

test("a line edit followed by Add, Move or Delete keeps the edit (the lines are read at click time)", () => {
  let doc = { nodes: [{ id: "l", t: "list", p: { items: "a\nb\n" } }] };
  const node = doc.nodes[0];
  const commit = (ops) => { for (const o of ops) doc = { nodes: [{ ...doc.nodes[0], p: { items: o.value } }] }; return true; };
  const api = { getDoc: () => doc, announce() {} };
  const el = itemsPanel(node, { api, commit, draw() {} });
  const find = (f) => { const out = []; el.walk((n) => f(n) && out.push(n)); return out[0]; };
  const input = find((n) => n.attrs["aria-label"] === "Item 3");
  input.value = "C";
  input.fire("change");
  find((n) => n.tag === "button" && n.textContent === "Add item").fire("click");
  assert.equal(doc.nodes[0].p.items, "a\nb\nC\n", "Add item kept the typed line");
  find((n) => n.tag === "button" && n.attrs["aria-label"] === "Move item 3 up").fire("click");
  assert.equal(doc.nodes[0].p.items, "a\nC\nb\n");
  find((n) => n.attrs["aria-label"] === "Item 1").value = "A";
  find((n) => n.attrs["aria-label"] === "Item 1").fire("change");
  find((n) => n.tag === "button" && n.attrs["aria-label"] === "Delete item 3").fire("click");
  assert.equal(doc.nodes[0].p.items, "A\nC\n", "the Item 1 edit survived the Delete");
});

test("pure helpers and kinds without items", () => {
  assert.equal(itemsPanel({ id: "x", t: "text" }, {}), null);
  assert.deepEqual(swapLines(["a", "b"], 0, -1), ["a", "b"]);
  assert.deepEqual(linesOf(""), []);
  assert.equal(itemName({ t: "image", p: { src: "/m/cat.png" } }, "alt", 0), "cat.png");
  assert.equal(itemName({ t: "tabpanel", p: {} }, "label", 1), "Item 2");
});
