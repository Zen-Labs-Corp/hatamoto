// The builder store: every op verb and its inverse, history, limits, compositions.
// Run: node --test scripts/admin-qa/unit/builder-store.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

const leaf = (id, t = "text", p = { text: id }) => ({ id, t, p });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const base = () => ({
  v: 2,
  nodes: [
    { id: "a", t: "container", s: { base: { gap: "m" } }, c: [leaf("a1"), leaf("a2", "heading", { text: "H", level: 2 })] },
    box("b", [box("b1", [leaf("b11")])]),
    box("e"),
  ],
});
const deep = (x) => JSON.parse(JSON.stringify(x));

// seeded PRNG (mulberry32)
const rng = (seed) => () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

const ops = [
  ["insert", { op: "insert", parent: "e", index: 0, node: leaf("x") }],
  ["insert top", { op: "insert", parent: "", index: 1, node: box("t") }],
  ["remove", { op: "remove", id: "a" }],
  ["move across parents", { op: "move", id: "a1", parent: "b1", index: 1 }],
  ["move within", { op: "move", id: "a1", parent: "a", index: 1 }],
  ["setProp new", { op: "setProp", id: "b", key: "gap", value: "x" }],
  ["setProp change", { op: "setProp", id: "a2", key: "level", value: 3 }],
  ["setProp delete", { op: "setProp", id: "a2", key: "level", value: null }],
  ["setProp delete last", { op: "setProp", id: "a1", key: "text", value: null }],
  ["setStyle new bp", { op: "setStyle", id: "a", bp: "md", key: "gap", value: "s" }],
  ["setStyle state", { op: "setStyle", id: "a", bp: "base:hover", key: "shadow", value: "lg" }],
  ["setStyle md state", { op: "setStyle", id: "a", bp: "md:parent-hover", key: "bg", value: "surface" }],
  ["setStyle change", { op: "setStyle", id: "a", bp: "base", key: "gap", value: "l" }],
  ["setStyle delete", { op: "setStyle", id: "a", bp: "base", key: "gap", value: null }],
];

for (const [name, op] of ops) {
  test(`${name}: applies and its inverse restores a deep-equal doc`, () => {
    const d0 = base();
    const snap = deep(d0);
    const { doc, inv } = S.applyOp(d0, op);
    assert.deepEqual(d0, snap, "input untouched");
    assert.notDeepEqual(doc, d0);
    assert.deepEqual(S.applyOp(doc, inv).doc, d0);
  });
}

test("path copy shares untouched subtrees", () => {
  const d0 = base();
  const { doc } = S.applyOp(d0, { op: "setProp", id: "a1", key: "text", value: "z" });
  assert.notEqual(doc.nodes[0], d0.nodes[0]);
  assert.equal(doc.nodes[1], d0.nodes[1]);
  assert.equal(doc.nodes[0].c[1], d0.nodes[0].c[1]);
});

test("refused ops throw and leave the doc unchanged", () => {
  const d0 = base();
  const snap = deep(d0);
  const nest = (n) => { let c = leaf("deepleaf"); for (let i = 0; i < n; i++) c = box("d" + i, [c]); return c; };
  const many = { v: 2, nodes: [box("big", Array.from({ length: 998 }, (_, i) => leaf("m" + i)))] };
  const bad = [
    [d0, { op: "move", id: "b", parent: "b", index: 0 }],
    [d0, { op: "move", id: "b", parent: "b11", index: 0 }],
    [d0, { op: "insert", parent: "a1", index: 0, node: leaf("y") }],
    [d0, { op: "insert", parent: "", index: 0, node: nest(8) }],
    [d0, { op: "insert", parent: "e", index: 3, node: leaf("y") }],
    [d0, { op: "insert", parent: "e", index: 0, node: leaf("a1") }],
    [d0, { op: "insert", parent: "e", index: 0, node: leaf("bad id!") }],
    [d0, { op: "insert", parent: "e", index: 0, node: { ...leaf("y"), c: [leaf("z")] } }],
    [d0, { op: "move", id: "a1", parent: "b", index: 9 }],
    [d0, { op: "remove", id: "nope" }],
    [d0, { op: "setStyle", id: "a", bp: "xl", key: "gap", value: "s" }],
    [d0, { op: "setStyle", id: "a", bp: "xl:hover", key: "gap", value: "s" }],
    [d0, { op: "setStyle", id: "a", bp: "base:visited", key: "gap", value: "s" }],
    [d0, { op: "setStyle", id: "a", bp: "base:", key: "gap", value: "s" }],
    [d0, { op: "setStyle", id: "a", bp: ":hover", key: "gap", value: "s" }],
    [d0, { op: "setStyle", id: "a", bp: "base:hover:focus", key: "gap", value: "s" }],
    [d0, { op: "frob" }],
    [many, { op: "insert", parent: "big", index: 0, node: box("pair", [leaf("pq")]) }],
  ];
  assert.doesNotThrow(() => S.applyOp(d0, { op: "insert", parent: "", index: 0, node: nest(7) }), "depth 8 is allowed");
  assert.doesNotThrow(() => S.applyOp(many, { op: "insert", parent: "big", index: 0, node: leaf("m1000") }), "1000 nodes is allowed");
  for (const [d, op] of bad) assert.throws(() => S.applyOp(d, op), undefined, JSON.stringify(op).slice(0, 60));
  assert.deepEqual(d0, snap);
});

test("undo/redo over 50 seeded random ops returns to the start and back", () => {
  const rand = rng(42);
  const pick = (a) => a[Math.floor(rand() * a.length)];
  let store = S.createStore(base());
  const start = deep(store.doc);
  let done = 0;
  for (let guard = 0; done < 50 && guard < 1000; guard++) {
    const all = S.flat(store.doc);
    const containers = all.filter((n) => n.t === "container");
    const n = pick(all);
    const k = rand();
    let op;
    if (k < 0.3) {
      const p = pick(containers);
      op = { op: "insert", parent: p.id, index: Math.floor(rand() * ((p.c ?? []).length + 1)), node: leaf(S.newId()) };
    } else if (k < 0.45) op = { op: "remove", id: n.id };
    else if (k < 0.7) {
      const p = pick(containers);
      op = { op: "move", id: n.id, parent: p.id, index: Math.floor(rand() * ((p.c ?? []).length + 1)) };
    } else if (k < 0.85) op = { op: "setProp", id: n.id, key: pick(["text", "gap", "level"]), value: rand() < 0.3 ? null : Math.floor(rand() * 9) };
    else op = { op: "setStyle", id: n.id, bp: pick(["base", "md", "sm"]), key: "gap", value: rand() < 0.3 ? null : pick(["s", "m", "l"]) };
    try { store = S.commit(store, [op], { now: done * 5000 }); done++; } catch { /* refused: try another */ }
  }
  assert.equal(done, 50);
  const end = deep(store.doc);
  while (S.canUndo(store)) store = S.undo(store);
  assert.deepEqual(store.doc, start);
  while (S.canRedo(store)) store = S.redo(store);
  assert.deepEqual(store.doc, end);
});

test("commit is atomic: a failing op in a batch changes nothing", () => {
  const s0 = S.createStore(base());
  assert.throws(() => S.commit(s0, [{ op: "remove", id: "a" }, { op: "remove", id: "a" }]));
  assert.equal(s0.past.length, 0);
  assert.ok(S.find(s0.doc, "a"));
});

test("a commit is one history entry; a new commit clears redo", () => {
  let s = S.createStore(base());
  s = S.commit(s, [{ op: "remove", id: "a1" }, { op: "remove", id: "a2" }], { label: "x" });
  assert.equal(s.past.length, 1);
  assert.equal(S.find(s.doc, "a"), s.doc.nodes[0]);
  s = S.undo(s);
  assert.deepEqual(s.doc, base());
  assert.ok(S.canRedo(s));
  s = S.commit(s, [{ op: "remove", id: "e" }]);
  assert.ok(!S.canRedo(s));
  assert.deepEqual(S.undo(S.createStore(base())).applied, []);
});

test("a drag session key merges setStyle commits for any pause: one undo step", () => {
  const set = (v) => [{ op: "setStyle", id: "a", bp: "base", key: "mt", value: v }];
  let s = S.createStore(base());
  for (let i = 0; i < 10; i++) s = S.commit(s, set(`${i}px`), { coalesce: "s:1:1", label: "Style", now: i * 500 }); // 5 s, 500 ms gaps
  s = S.commit(s, set("20px"), { coalesce: "s:1:1", now: 9000 }); // a long pause mid-drag does not split
  assert.equal(s.past.length, 1);
  assert.equal(S.find(s.doc, "a").s?.base?.mt, "20px");
  const undone = S.undo(s);
  assert.equal(S.find(undone.doc, "a").s?.base?.mt, undefined, "undo restores the value from before the drag");
  assert.equal(S.find(S.redo(undone).doc, "a").s.base.mt, "20px");
});

test("session keys: different sessions and plain commits do not merge; undo and redo end a session", () => {
  const set = (v) => [{ op: "setStyle", id: "a", bp: "base", key: "mt", value: v }];
  let s = S.createStore(base());
  s = S.commit(s, set("1px"), { coalesce: "s:1:1", now: 0 });
  s = S.commit(s, set("2px"), { coalesce: "s:1:2", now: 1 });
  assert.equal(s.past.length, 2, "a new session is a new step");
  s = S.commit(s, set("3px"), { now: 2 });
  s = S.commit(s, set("4px"), { coalesce: "s:1:2", now: 3 });
  assert.equal(s.past.length, 4, "a plain commit in between ends the session");
  s = S.redo(S.undo(s));
  s = S.commit(s, set("5px"), { coalesce: "s:1:2", now: 4 });
  assert.equal(s.past.length, 5, "after undo/redo the session key does not merge");
});

test("a batch under one session key (linked box sides) is one undo step", () => {
  const four = (v) => ["pt", "pr", "pb", "pl"].map((key) => ({ op: "setStyle", id: "a", bp: "base", key, value: v }));
  let s = S.createStore(base());
  for (let i = 1; i <= 5; i++) s = S.commit(s, four(`${i}px`), { coalesce: "s:2:1", now: i * 3000 });
  assert.equal(s.past.length, 1);
  const before = S.undo(s).doc;
  assert.equal(S.find(before, "a").s?.base?.pt, undefined);
  assert.equal(S.find(S.redo(S.undo(s)).doc, "a").s.base.pl, "5px");
});

test("coalesce:true never merges setStyle, so each arrow-key press is its own undo step", () => {
  const set = (v) => [{ op: "setStyle", id: "a", bp: "base", key: "mt", value: v }];
  let s = S.createStore(base());
  for (let i = 0; i < 4; i++) s = S.commit(s, set(`${i}px`), { coalesce: i % 2 === 0, now: i });
  for (let i = 4; i < 8; i++) s = S.commit(s, set(`${i}px`), { coalesce: false, now: i });
  assert.equal(s.past.length, 8);
});

test("coalescing merges setProp on the same id+key within 1s only", () => {
  const set = (v) => [{ op: "setProp", id: "a1", key: "text", value: v }];
  let s = S.createStore(base());
  s = S.commit(s, set("h"), { coalesce: true, now: 0 });
  s = S.commit(s, set("he"), { coalesce: true, now: 900 });
  s = S.commit(s, set("hey"), { coalesce: true, now: 1800 });
  assert.equal(s.past.length, 1);
  s = S.commit(s, set("hey!"), { coalesce: true, now: 3000 });
  assert.equal(s.past.length, 2);
  s = S.commit(s, [{ op: "setProp", id: "a1", key: "other", value: 1 }], { coalesce: true, now: 3100 });
  assert.equal(s.past.length, 3);
  s = S.commit(s, set("no"), { coalesce: false, now: 3200 });
  assert.equal(s.past.length, 4);
  const merged = S.createStore(base());
  let m = S.commit(merged, set("a"), { coalesce: true, now: 0 });
  m = S.commit(m, set("ab"), { coalesce: true, now: 10 });
  assert.equal(S.find(S.undo(m).doc, "a1").p.text, "a1", "undo restores the value from before typing");
  assert.equal(S.find(S.redo(S.undo(m)).doc, "a1").p.text, "ab");
});

test("duplicate gives fresh unique ids for every descendant", () => {
  const d0 = base();
  const [op] = S.duplicateOps(d0, "a");
  assert.deepEqual([op.op, op.parent, op.index], ["insert", "", 1]);
  const ids = S.flat({ nodes: [op.node] }).map((n) => n.id);
  const old = S.flat({ nodes: [d0.nodes[0]] }).map((n) => n.id);
  assert.equal(ids.length, old.length);
  assert.equal(new Set(ids).size, ids.length);
  for (const id of ids) { assert.ok(!old.includes(id)); assert.match(id, /^n[0-9a-f]{10}$/); }
  const { doc } = S.applyOp(d0, op);
  assert.equal(S.flat(doc).length, S.flat(d0).length + old.length);
  assert.equal(doc.nodes[1].s.base.gap, "m");
  op.node.s.base.gap = "zzz";
  assert.equal(d0.nodes[0].s.base.gap, "m", "copy shares nothing");
});

test("wrap then unwrap restores the doc; wrap works nested and on undo", () => {
  for (const id of ["a2", "a", "b11"]) {
    const d0 = base();
    let s = S.commit(S.createStore(d0), S.wrapOps(d0, id));
    const loc = S.locate(s.doc, id);
    assert.equal(loc.parent.t, "container");
    assert.equal(loc.parent.c.length, 1);
    const wid = loc.parent.id;
    assert.deepEqual(S.locate(d0, id).parentId, S.locate(s.doc, wid).parentId);
    s = S.commit(s, S.unwrapOps(s.doc, wid));
    assert.deepEqual(s.doc, d0);
    s = S.undo(S.undo(s));
    assert.deepEqual(s.doc, d0);
  }
});

test("queries: find, flat, topOf, siblings, v1Shaped", () => {
  const d = base();
  assert.equal(S.find(d, "b11").id, "b11");
  assert.equal(S.find(d, "zz"), null);
  assert.deepEqual(S.flat(d).map((n) => n.id), ["a", "a1", "a2", "b", "b1", "b11", "e"]);
  assert.equal(S.topOf(d, "b11").id, "b");
  assert.deepEqual(S.siblings(d, "b").map((n) => n.id), ["b1"]);
  assert.deepEqual(S.siblings(d, "").length, 3);
  const sec = { id: "s", t: "container", p: { tag: "section" }, c: [{ id: "c", t: "component", p: { type: "hero" } }] };
  assert.equal(S.v1Shaped(sec), true);
  assert.equal(S.v1Shaped({ ...sec, p: { tag: "div" } }), false);
  assert.equal(S.v1Shaped({ ...sec, p: { tag: "section", layout: "grid" } }), false);
  assert.equal(S.v1Shaped({ ...sec, p: { hidden: true, v1: true } }), true);
  assert.equal(S.v1Shaped({ ...sec, s: { base: { gap: "s" } } }), false);
  assert.equal(S.v1Shaped({ ...sec, c: [sec.c[0], leaf("q")] }), false);
  assert.equal(S.v1Shaped({ ...sec, c: [{ ...sec.c[0], s: { base: { gap: "s" } } }] }), false);
  assert.equal(S.v1Shaped(leaf("t")), false);
});

test("unchanged compares against the doc's current value, null meaning absent", () => {
  const d = base();
  assert.equal(S.unchanged(d, "a2", "level", 2), true);
  assert.equal(S.unchanged(d, "a2", "level", 3), false);
  assert.equal(S.unchanged(d, "a2", "alt", null), true);
  const st = S.commit(S.createStore(d), [{ op: "setProp", id: "a2", key: "text", value: "H!" }]);
  assert.equal(S.unchanged(st.doc, "a2", "text", "H"), false, "typing back to the original is a change from what the store holds");
  assert.equal(S.unchanged(st.doc, "a2", "text", "H!"), true);
});

test("canEditText allows only a text prop the node's kind has", () => {
  const kinds = [{ kind: "heading", props: [{ key: "text" }, { key: "level" }] }, { kind: "image", props: [{ key: "src" }] }];
  assert.equal(S.canEditText(kinds, { id: "x", t: "heading" }, "text"), true);
  assert.equal(S.canEditText(kinds, { id: "x", t: "heading" }, "level"), false);
  assert.equal(S.canEditText(kinds, { id: "x", t: "image" }, "text"), false);
  assert.equal(S.canEditText(kinds, { id: "x", t: "image" }, "src"), false);
  assert.equal(S.canEditText(kinds, null, "text"), false);
  // html: only for a kind that declares the prop (heading/text, never button)
  const withHTML = [{ kind: "heading", props: [{ key: "text" }, { key: "html" }] }, { kind: "button", props: [{ key: "text" }] }];
  assert.equal(S.canEditText(withHTML, { id: "x", t: "heading" }, "html"), true);
  assert.equal(S.canEditText(withHTML, { id: "x", t: "button" }, "html"), false);
  assert.equal(S.canEditText(kinds, { id: "x", t: "heading" }, "html"), false);
  // richtext and the html widget declare an html prop too, but a canvas message may not rewrite their markup
  const widgets = [{ kind: "richtext", props: [{ key: "html" }] }, { kind: "html", props: [{ key: "html" }] }, { kind: "text", props: [{ key: "text" }, { key: "html" }] }];
  assert.equal(S.canEditText(widgets, { id: "x", t: "richtext" }, "html"), false);
  assert.equal(S.canEditText(widgets, { id: "x", t: "html" }, "html"), false);
  assert.equal(S.canEditText(widgets, { id: "x", t: "text" }, "html"), true);
});

test("child kinds come from the server table: tabs take children once configured", () => {
  const doc = { v: 2, nodes: [{ id: "t", t: "tabs" }] };
  const op = { op: "insert", parent: "t", index: 0, node: { id: "p", t: "tabpanel" } };
  assert.throws(() => S.applyOp(doc, op), /cannot hold/);
  S.setChildKinds([{ kind: "container", children: true }, { kind: "tabs", children: true }]);
  try {
    assert.equal(S.holdsChildren("tabs"), true);
    assert.equal(S.holdsChildren("text"), false);
    assert.equal(S.applyOp(doc, op).doc.nodes[0].c[0].id, "p");
  } finally {
    S.setChildKinds([{ kind: "container", children: true }]);
  }
});

test("parent-hover styles need a parent element, as the server requires", () => {
  const ph = (id) => ({ ...leaf(id), s: { "base:parent-hover": { opacity: "1" } } });
  const d = { v: 2, nodes: [box("p", [ph("k")]), { id: "r", t: "ref", p: { block: "b" } }, box("e")] };
  for (const op of [
    { op: "move", id: "k", parent: "", index: 0 },
    { op: "insert", parent: "", index: 0, node: ph("n") },
    { op: "insert", parent: "", index: 0, node: { ...box("w", [leaf("x")]), s: { "md:parent-hover": { opacity: "1" } } } },
  ]) assert.throws(() => S.applyOp(d, op), /Parent hover/);
  S.applyOp(d, { op: "move", id: "k", parent: "e", index: 0 });
  S.applyOp(d, { op: "insert", parent: "e", index: 0, node: ph("n") });
});

test("takesParentHover: a real element parent only", () => {
  assert.equal(S.takesParentHover(null), false);
  assert.equal(S.takesParentHover({ t: "ref" }), false);
  assert.equal(S.takesParentHover({ t: "component" }), false);
  assert.equal(S.takesParentHover({ t: "container" }), true);
});

test("jump(-n) and jump(+n) land on the same doc as n undos and redos, in one batch", () => {
  const d0 = base();
  let s = S.createStore(d0);
  const labels = ["one", "two", "three", "four"];
  const steps = [
    [{ op: "insert", parent: "e", index: 0, node: leaf("x") }],
    [{ op: "setProp", id: "a1", key: "text", value: "A" }],
    [{ op: "move", id: "a2", parent: "b", index: 1 }],
    [{ op: "remove", id: "b11" }],
  ];
  steps.forEach((ops, i) => (s = S.commit(s, ops, { label: labels[i] })));
  for (const n of [1, 2, 3, 4]) {
    let one = s;
    const ran = [];
    for (let i = 0; i < n; i++) {
      one = S.undo(one);
      ran.push(...one.applied);
    }
    const j = S.jump(s, -n);
    assert.deepEqual(j.doc, one.doc, `undo ${n}`);
    assert.deepEqual(j.past, one.past);
    assert.deepEqual(j.future, one.future);
    assert.deepEqual(j.applied, ran, "applied is the concatenation, in run order");
    let back = j;
    const fwd = [];
    for (let i = 0; i < n; i++) {
      back = S.redo(back);
      fwd.push(...back.applied);
    }
    const r = S.jump(j, n);
    assert.deepEqual(r.doc, back.doc, `redo ${n}`);
    assert.deepEqual(r.applied, fwd);
    assert.deepEqual(r.doc, s.doc);
  }
});

test("jump: 0 is a no-op, overshoot clamps, a new commit after a jump drops the future", () => {
  let s = S.createStore(base());
  s = S.commit(s, [{ op: "remove", id: "e" }], { label: "r" });
  s = S.commit(s, [{ op: "remove", id: "b" }], { label: "r2" });
  const z = S.jump(s, 0);
  assert.deepEqual(z.doc, s.doc);
  assert.deepEqual(z.applied, []);
  const all = S.jump(s, -99);
  assert.deepEqual(all.doc, base());
  assert.equal(all.past.length, 0);
  assert.equal(all.future.length, 2);
  assert.deepEqual(S.jump(all, 99).doc, s.doc);
  assert.deepEqual(S.jump(s, 5).applied, [], "nothing to redo");
  const mid = S.jump(s, -1);
  assert.equal(S.commit(mid, [{ op: "remove", id: "a" }], { label: "x" }).future.length, 0);
});

test("moveSubject: a v1 section's component moves as its container; anything else moves as itself", () => {
  const sec = { id: "s1", t: "container", p: { tag: "section" }, c: [{ id: "c1", t: "component", p: { type: "hero" } }] };
  const nested = { id: "w", t: "container", c: [{ id: "c2", t: "component", p: { type: "hero" } }, leaf("x")] };
  const doc = { v: 2, nodes: [sec, nested] };
  assert.equal(S.moveSubject(doc, "c1"), "s1");
  assert.equal(S.moveSubject(doc, "s1"), "s1");
  assert.equal(S.moveSubject(doc, "c2"), "c2", "a component inside a plain container is its own subject");
  assert.equal(S.moveSubject(doc, "x"), "x");
  assert.equal(S.moveSubject(doc, "gone"), "gone");
});

test("v1Shaped ignores navlabel (renaming a v1 section keeps it v1)", () => {
  const sec = { id: "s", t: "container", p: { tag: "section" }, c: [{ id: "c", t: "component", p: { type: "hero" } }] };
  const d = S.createStore({ v: 2, nodes: [sec] });
  S.commit(d, [{ op: "setProp", id: "s", key: "navlabel", value: "Hero" }], "rename");
  assert.equal(S.v1Shaped(d.doc.nodes[0]), true);
});

test("ownsText: Enter edits only an element whose own text is on the canvas, never a container's descendant", () => {
  for (const t of ["heading", "text", "button"]) assert.equal(S.ownsText({ id: "x", t }), true, t);
  assert.equal(S.ownsText({ id: "x", t: "container", c: [{ id: "y", t: "heading" }] }), false);
  assert.equal(S.ownsText({ id: "x", t: "image" }), false);
  assert.equal(S.ownsText({ id: "x", t: "text", b: { text: "title" } }), false);
  assert.equal(S.ownsText(null), false);
});
