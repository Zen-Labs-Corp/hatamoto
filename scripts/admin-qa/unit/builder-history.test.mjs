// The history panel rows. Run: node --test scripts/admin-qa/unit/builder-history.test.mjs
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
const H = await import(new URL("../../../backend/web/admin/js/builder/history.js", import.meta.url));
const S = await import(new URL("../../../backend/web/admin/js/builder/store.js", import.meta.url));

const T0 = 1_000_000;
const leaf = (id) => ({ id, t: "text", p: { text: id } });
const ins = (id) => [{ op: "insert", parent: "", index: 0, node: leaf(id) }];
function session() {
  let s = S.createStore({ v: 2, nodes: [] });
  s = S.commit(s, ins("a"), { label: "Add", now: T0 });
  s = S.commit(s, ins("b"), { label: "", now: T0 + 30_000 });
  s = S.commit(s, ins("c"), { label: "Duplicate", now: T0 + 400_000 });
  return s;
}

test("ago: units and rounding", () => {
  assert.equal(H.ago(0), "just now");
  assert.equal(H.ago(4_999), "just now");
  assert.equal(H.ago(40_000), "40 s ago");
  assert.equal(H.ago(5 * 60_000), "5 min ago");
  assert.equal(H.ago(2 * 3600_000), "2 h ago");
  assert.equal(H.ago(-5), "just now", "a clock that went back");
});

test("historyRows: Original, the changes oldest first, the current one marked, blank labels named", () => {
  const rows = H.historyRows(session(), T0 + 400_000 + 90_000);
  assert.deepEqual(rows.map((r) => [r.n, r.label, r.current]), [[-3, "Original", false], [-2, "Add", false], [-1, "Change", false], [0, "Duplicate", true]]);
  assert.equal(rows[3].ago, "2 min ago");
  assert.equal(rows[1].ago, "8 min ago");
});

test("undone entries follow the current row, nearest redo first", () => {
  const s = S.undo(S.undo(session()));
  const rows = H.historyRows(s, T0 + 500_000);
  assert.deepEqual(rows.map((r) => [r.n, r.label, r.current]), [[-1, "Original", false], [0, "Add", true], [1, "Change", false], [2, "Duplicate", false]]);
});

test("every row's n lands on its state through store.jump", () => {
  const s = S.undo(session());
  const ids = (st) => st.doc.nodes.map((n) => n.id).join("");
  const want = { "-2": "", "-1": "a", 0: "ba", 1: "cba" };
  const rows = H.historyRows(s, T0);
  assert.equal(rows.length, 4);
  for (const r of rows) assert.equal(ids(S.jump(s, r.n)), want[r.n], `n=${r.n}`);
  assert.equal(H.historyRows(S.createStore({ v: 2, nodes: [] }), T0).length, 1, "a fresh store has only the original, current");
});
