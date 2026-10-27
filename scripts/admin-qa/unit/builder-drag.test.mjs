// Drop targets from reported rects (pure) and the palette's pure parts.
// Run: node --test scripts/admin-qa/unit/builder-drag.test.mjs
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
const { dropTarget, prepare, toDoc } = await import(new URL("../../../backend/web/admin/js/builder/drag.js", import.meta.url));
const { itemsOf, makeNode, insertPoint } = await import(new URL("../../../backend/web/admin/js/builder/palette.js", import.meta.url));
const { filterItems } = await import(new URL("../../../backend/web/admin/js/builder/search.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

const leaf = (id) => ({ id, t: "text", p: { text: id } });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const r = (id, x, y, w, h, extra = {}) => ({ id, k: "n", t: "text", p: "", x, y, w, h, ...extra });
const cont = (id, x, y, w, h, dir = "column") => r(id, x, y, w, h, { t: "container", c: 1, dir });

// page: A (column, 0..300) holds a1 (y 20-60), a2 (y 70-110); B (row, 320..420) holds b1 | b2; E empty container 440..520.
const doc = () => ({ v: 2, nodes: [box("A", [leaf("a1"), leaf("a2")]), box("B", [leaf("b1"), leaf("b2")]), box("E")] });
const nodes = () => [
  cont("A", 0, 0, 1000, 300), r("a1", 20, 20, 960, 40), r("a2", 20, 70, 960, 40),
  cont("B", 0, 320, 1000, 100, "row"), r("b1", 20, 330, 400, 80), r("b2", 500, 330, 400, 80),
  cont("E", 0, 440, 1000, 80),
];
const target = (pt, drag, opts, d = doc(), n = nodes()) => dropTarget(pt, prepare(d, n), drag, opts);
const fresh = { node: { id: "new", t: "text", p: { text: "x" } } };

test("column container: before/after a sibling by the midpoint, indicator is a horizontal line", () => {
  let t = target({ x: 100, y: 30 }, fresh);
  assert.deepEqual([t.parent, t.index], ["A", 0]);
  assert.deepEqual(t.line, { x: 20, y: 20, w: 960, h: 0 });
  t = target({ x: 100, y: 50 }, fresh);
  assert.deepEqual([t.parent, t.index], ["A", 1]);
  assert.deepEqual(t.line, { x: 20, y: 70, w: 960, h: 0 });
  t = target({ x: 100, y: 200 }, fresh);
  assert.deepEqual([t.parent, t.index], ["A", 2]);
  assert.deepEqual(t.line, { x: 20, y: 110, w: 960, h: 0 });
  assert.equal(t.op.op, "insert");
});

test("row container: left/right halves, vertical indicator", () => {
  let t = target({ x: 100, y: 370 }, fresh);
  assert.deepEqual([t.parent, t.index], ["B", 0]);
  assert.deepEqual(t.line, { x: 20, y: 330, w: 0, h: 80 });
  t = target({ x: 400, y: 370 }, fresh);
  assert.deepEqual([t.parent, t.index], ["B", 1]);
  t = target({ x: 800, y: 370 }, fresh);
  assert.deepEqual([t.parent, t.index], ["B", 2]);
  assert.deepEqual(t.line, { x: 900, y: 330, w: 0, h: 80 });
});

test("empty container: inside, whole box", () => {
  const t = target({ x: 500, y: 480 }, fresh);
  assert.deepEqual([t.parent, t.index, t.box], ["E", 0, true]);
  assert.deepEqual(t.line, { x: 0, y: 440, w: 1000, h: 80 });
});

test("top level: gaps between containers, and an empty page", () => {
  let t = target({ x: 500, y: 430 }, fresh); // the gap between B and E
  assert.deepEqual([t.parent, t.index], ["", 2]);
  assert.equal(t.line.h, 0);
  t = target({ x: 500, y: 600 }, fresh); // below everything
  assert.deepEqual([t.parent, t.index], ["", 3]);
  t = dropTarget({ x: 10, y: 10 }, prepare({ v: 2, nodes: [] }, []), fresh, { main: { x: 0, y: 64, w: 1280, h: 80 } });
  assert.deepEqual([t.parent, t.index, t.box], ["", 0, true]);
  assert.equal(t.line.y, 64);
});

test("a container's border zone means next to it, its centre means inside", () => {
  let t = target({ x: 500, y: 322 }, fresh); // top 8px of B (a top-level container)
  assert.deepEqual([t.parent, t.index], ["", 1]);
  t = target({ x: 500, y: 418 }, fresh); // bottom edge of B
  assert.deepEqual([t.parent, t.index], ["", 2]);
  t = target({ x: 500, y: 370 }, fresh);
  assert.equal(t.parent, "B");
});

test("moving: index counts the siblings without the dragged node", () => {
  // a1 dragged below a2: list without a1 is [a2], so after a2 = index 1
  const t = target({ x: 100, y: 100 }, { id: "a1" });
  assert.deepEqual([t.parent, t.index, t.op.op], ["A", 1, "move"]);
  // a1 dragged onto its own top half: stays at index 0
  const same = target({ x: 100, y: 30 }, { id: "a1" });
  assert.deepEqual([same.parent, same.index], ["A", 0]);
  const into = target({ x: 300, y: 370 }, { id: "a2" }); // into row B between b1 and b2
  assert.deepEqual([into.parent, into.index], ["B", 1]);
});

test("self and descendants are never a target: a container falls back to its parent level", () => {
  // dragging A over A's own children: the only containers under the point are A itself, so the top level wins
  const t = target({ x: 100, y: 30 }, { id: "A" });
  assert.equal(t.parent, "");
  assert.ok(!["A", "a1", "a2"].includes(t.parent));
  const nested = {
    doc: { v: 2, nodes: [box("P", [box("Q", [leaf("q1")]), leaf("p2")])] },
    nodes: [cont("P", 0, 0, 800, 400), cont("Q", 20, 20, 500, 200), r("q1", 30, 30, 400, 40), r("p2", 20, 240, 760, 40)],
  };
  const t2 = target({ x: 100, y: 50 }, { id: "P" }, {}, nested.doc, nested.nodes);
  assert.equal(t2.parent, "");
  const t3 = target({ x: 100, y: 100 }, { id: "Q" }, {}, nested.doc, nested.nodes); // over Q itself: Q is skipped, P takes it
  assert.equal(t3.parent, "P");
});

test("depth cap: a placement the store refuses is skipped for the next candidate", () => {
  // a chain of 8 nested containers; a new container inside the deepest would be depth 9
  let d = box("d8");
  for (let i = 7; i >= 1; i--) d = box("d" + i, [d]);
  const doc8 = { v: 2, nodes: [d] };
  const rects = [];
  for (let i = 1; i <= 8; i++) rects.push(cont("d" + i, i * 10, i * 10, 1000 - i * 20, 800 - i * 20));
  const t = target({ x: 400, y: 400 }, { node: { id: "z", t: "container" } }, {}, doc8, rects);
  assert.notEqual(t.parent, "d8");
  assert.ok(t.parent === "d7" || t.parent === "d6" || t.parent === "");
  S.applyOp(doc8, t.op); // what we return always applies
  // a leaf inside a leaf is not a candidate at all
  const leafOnly = target({ x: 30, y: 30 }, fresh);
  assert.notEqual(leafOnly.parent, "a1");
});

test("every returned op applies to the doc", () => {
  for (const pt of [{ x: 100, y: 30 }, { x: 100, y: 370 }, { x: 500, y: 480 }, { x: 500, y: 700 }, { x: 5, y: 5 }]) {
    for (const drag of [fresh, { id: "a1" }, { id: "B" }, { id: "b2" }]) {
      const t = target(pt, drag);
      assert.ok(t);
      S.applyOp(doc(), t.op);
    }
  }
});

test("point conversion at scale 0.5 and 1", () => {
  assert.deepEqual(toDoc(300, 250, { left: 100, top: 50 }, 0.5), { x: 400, y: 400 });
  assert.deepEqual(toDoc(300, 250, { left: 100, top: 50 }, 1), { x: 200, y: 200 });
  // the border zone is 8 screen px: 16 doc px at 0.5
  const t = target({ x: 500, y: 335 }, fresh, { scale: 0.5 }); // 15 doc px below B's top edge
  assert.deepEqual([t.parent, t.index], ["", 1]);
  const inside = target({ x: 500, y: 335 }, fresh, { scale: 1 });
  assert.equal(inside.parent, "B");
});

const kinds = [
  { kind: "container", children: true, props: [], default: { tag: "div" } },
  { kind: "heading", children: false, props: [], default: { text: "Heading", level: 2 } },
  { kind: "spacer", children: false, props: [] },
  { kind: "component", children: false, props: [] },
];
const sections = [{ type: "hero", title: "Hero" }, { type: "faq", title: "" }];

test("palette items, filter and node factory", () => {
  const items = itemsOf(kinds, sections);
  assert.deepEqual(items.map((i) => i.key), ["container", "heading", "spacer", "component:hero", "component:faq"]);
  assert.equal(items[4].name, "Faq");
  assert.deepEqual(filterItems(items, "hero").map((i) => i.key), ["component:hero"]);
  assert.deepEqual(filterItems(items, "theme section").length, 0, "the hint 'Theme section' is not searchable");
  assert.equal(filterItems(items, "zzz").length, 0);
  assert.equal(filterItems(items, "  ").length, 5);
  const h = makeNode(items[1], kinds);
  assert.deepEqual([h.t, h.p], ["heading", { text: "Heading", level: 2 }]);
  assert.match(h.id, /^n[0-9a-f]{10}$/);
  assert.equal(makeNode(items[2], kinds).p, undefined);
  assert.deepEqual(makeNode(items[3], kinds).p, { type: "hero" });
  assert.notEqual(makeNode(items[1], kinds).p, makeNode(items[1], kinds).p); // fresh copy each time
});

test("insertPoint: end of page, after a leaf, inside a container, after a theme section wrapper", () => {
  const d = { v: 2, nodes: [box("A", [leaf("a1"), leaf("a2")]), { id: "s", t: "container", p: { tag: "section" }, c: [{ id: "k", t: "component", p: { type: "hero" } }] }, leaf("z")] };
  assert.deepEqual(insertPoint(d, null), { parent: "", index: 3 });
  assert.deepEqual(insertPoint(d, "a1"), { parent: "A", index: 1 });
  assert.deepEqual(insertPoint(d, "A"), { parent: "A", index: 2 });
  assert.deepEqual(insertPoint(d, "s"), { parent: "", index: 2 });
  assert.deepEqual(insertPoint(d, "k"), { parent: "s", index: 1 });
  assert.deepEqual(insertPoint(d, "gone"), { parent: "", index: 3 });
});

test("widget kinds: label, hint and group from the server, seed subtree, children-aware insert point", () => {
  const ks = [
    { kind: "container", children: true, props: [], default: { tag: "div" } },
    { kind: "tabs", children: true, props: [], label: "Tabs", hint: "Panels", group: "Interactive", seed: [{ t: "tabpanel", p: { label: "Tab 1" } }, { t: "tabpanel", p: { label: "Tab 2" } }] },
    { kind: "tabpanel", children: true, props: [], group: "Interactive" },
    { kind: "stat", children: false, props: [], label: "Stat", group: "Content", default: { value: "1" } },
  ];
  const items = itemsOf(ks, []);
  assert.deepEqual(items.map((i) => [i.name, i.group]), [["Container", "Elements"], ["Tabs", "Interactive"], ["Tab panel", "Interactive"], ["Stat", "Content"]]);
  const t = makeNode(items[1], ks);
  assert.equal(t.c.length, 2);
  assert.deepEqual(t.c.map((c) => [c.t, c.p]), [["tabpanel", { label: "Tab 1" }], ["tabpanel", { label: "Tab 2" }]]);
  assert.equal(new Set([t.id, ...t.c.map((c) => c.id)]).size, 3);
  assert.equal(makeNode(items[3], ks).c, undefined);
  const d = { nodes: [{ id: "T", t: "tabs", c: [] }, { id: "S", t: "stat" }] };
  assert.deepEqual(insertPoint(d, "T", ks), { parent: "T", index: 0 });
  assert.deepEqual(insertPoint(d, "S", ks), { parent: "", index: 2 });
});
