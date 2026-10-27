// Number of columns: the count and the child containers change in one commit, content is never removed.
// Run: node --test scripts/admin-qa/unit/builder-columnsops.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register("data:text/javascript," + encodeURIComponent(`export async function resolve(s, c, next) { if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true }; return next(s, c); }`));
const { columnsOps, REFUSE } = await import(new URL("builder/columnsops.js", root));
const S = await import(new URL("builder/store.js", root));

const cols = (kids, count = kids.length) => ({ id: "c", t: "columns", p: { count }, c: kids });
const empty = (id) => ({ id, t: "container" });

test("adding columns appends empty containers and sets the count, in one op list", () => {
  const { ops } = columnsOps(cols([empty("a"), empty("b")]), 4);
  assert.deepEqual(ops.map((o) => o.op), ["setProp", "insert", "insert"]);
  assert.deepEqual(ops.slice(1).map((o) => [o.parent, o.index, o.node.t]), [["c", 2, "container"], ["c", 3, "container"]]);
  assert.equal(new Set(ops.slice(1).map((o) => o.node.id)).size, 2, "fresh ids");
});

test("removing columns takes the empty trailing ones", () => {
  const { ops } = columnsOps(cols([empty("a"), empty("b"), empty("c2")]), 2);
  assert.deepEqual(ops, [{ op: "setProp", id: "c", key: "count", value: 2 }, { op: "remove", id: "c2" }]);
});

test("a trailing column with content is refused, nothing is removed", () => {
  const full = { id: "c2", t: "container", c: [{ id: "t", t: "text" }] };
  assert.deepEqual(columnsOps(cols([empty("a"), empty("b"), full]), 2), { refuse: REFUSE });
});

test("a trailing column that is not a container (a heading placed at the Columns level) is content and is refused", () => {
  assert.deepEqual(columnsOps(cols([empty("a"), empty("b"), { id: "h", t: "heading", p: { text: "Pricing" } }]), 2), { refuse: REFUSE });
});

test("no change commits nothing", () => {
  assert.deepEqual(columnsOps(cols([empty("a"), empty("b")]), 2), { ops: [] });
});

test("the ops apply to a document and one undo restores it", () => {
  S.setChildKinds([{ kind: "container", children: true }, { kind: "columns", children: true }]);
  const doc = { nodes: [cols([empty("a"), empty("b")])] };
  const { ops } = columnsOps(doc.nodes[0], 3);
  let next = doc, inv = [];
  for (const op of ops) { const r = S.applyOp(next, op); next = r.doc; inv.unshift(r.inv); }
  assert.equal(next.nodes[0].c.length, 3);
  assert.equal(next.nodes[0].p.count, 3);
  for (const op of inv) next = S.applyOp(next, op).doc;
  assert.deepEqual(next, doc);
});
