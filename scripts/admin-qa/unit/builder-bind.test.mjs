// Binding ops: bind/unbind are one undo step, source filtering follows the class matrix.
// Run: node --test scripts/admin-qa/unit/builder-bind.test.mjs
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
const S = await import("@hm/builder/store");
const B = await import("@hm/builder/bindops");

const doc = () => ({ v: 2, nodes: [{ id: "h", t: "heading", p: { text: "Hi", level: 2 } }] });
const sources = [
  { source: "entity.name", class: "text" },
  { source: "entity.url", class: "url" },
  { source: "entity.image", class: "image" },
  { source: "entity.attributes.<path>", class: "text" },
];

test("bind sets b, drops the literal, and undo restores both in one step", () => {
  const st = S.commit(S.createStore(doc()), B.bindOps("h", "text", "entity.name"));
  const n = st.doc.nodes[0];
  assert.deepEqual(n.b, { text: "entity.name" });
  assert.equal("text" in n.p, false);
  assert.equal(n.p.level, 2);
  assert.equal(st.past.length, 1);
  const u = S.undo(st);
  assert.deepEqual(u.doc.nodes[0], doc().nodes[0]);
  assert.deepEqual(S.redo(u).doc.nodes[0], n);
});

test("unbind deletes the empty b map; undo brings the binding back", () => {
  const bound = S.commit(S.createStore(doc()), B.bindOps("h", "text", "entity.name"));
  const un = S.commit(bound, B.unbindOps("h", "text"));
  assert.equal("b" in un.doc.nodes[0], false);
  assert.deepEqual(S.undo(un).doc.nodes[0].b, { text: "entity.name" });
});

test("chipText reads the source or empty", () => {
  const st = S.commit(S.createStore(doc()), B.bindOps("h", "text", "loop.item.title"));
  assert.equal(B.chipText(st.doc.nodes[0], "text"), "loop.item.title");
  assert.equal(B.chipText(st.doc.nodes[0], "level"), "");
  assert.equal(B.chipText(null, "text"), "");
});

test("sourcesFor: text takes all, url/image take their class plus attribute paths", () => {
  const names = (c) => B.sourcesFor(sources, c).map((s) => s.source);
  assert.equal(names("text").length, 4);
  assert.deepEqual(names("url"), ["entity.url", "entity.attributes.<path>"]);
  assert.deepEqual(names("image"), ["entity.image", "entity.attributes.<path>"]);
  assert.deepEqual(B.sourcesFor(undefined, "text"), []);
});

test("setBind rejects unknown nodes and non-string values", () => {
  assert.throws(() => S.applyOp(doc(), { op: "setBind", id: "zz", key: "text", value: "x" }), /no such node/);
  assert.throws(() => S.applyOp(doc(), { op: "setBind", id: "h", key: "text", value: 3 }), /bind source/);
});

test("clone and freshCopy keep b independent of the original", () => {
  const src = S.commit(S.createStore(doc()), B.bindOps("h", "text", "entity.name")).doc.nodes[0];
  const copy = S.freshCopy(src);
  assert.deepEqual(copy.b, src.b);
  copy.b.text = "entity.title";
  assert.equal(src.b.text, "entity.name");
  const ins = S.applyOp({ v: 2, nodes: [] }, { op: "insert", parent: "", index: 0, node: src });
  ins.doc.nodes[0].b.text = "x";
  assert.equal(src.b.text, "entity.name");
});

test("a setBind inside a coalesced run does not merge", () => {
  let st = S.createStore(doc());
  st = S.commit(st, [{ op: "setProp", id: "h", key: "text", value: "a" }], { coalesce: true, now: 1000 });
  st = S.commit(st, B.bindOps("h", "text", "entity.name"), { coalesce: true, now: 1100 });
  st = S.commit(st, [{ op: "setProp", id: "h", key: "text", value: "b" }], { coalesce: true, now: 1200 });
  assert.equal(st.past.length, 3);
});

test("inLoop and sourcesFor: loop.item.* is offered only inside a loop", () => {
  const doc = { nodes: [{ id: "a", t: "heading" }, { id: "l", t: "loop", c: [{ id: "c", t: "container", c: [{ id: "h", t: "heading" }] }] }] };
  assert.equal(B.inLoop(doc, "a"), false);
  assert.equal(B.inLoop(doc, "l"), false);
  assert.equal(B.inLoop(doc, "h"), true);
  const src = [{ source: "entity.name", class: "text" }, { source: "loop.item.title", class: "text" }];
  assert.deepEqual(B.sourcesFor(src, "text", false).map((s) => s.source), ["entity.name"]);
  assert.equal(B.sourcesFor(src, "text").length, 2);
});
