// Pure menu tree operations, checked against the cases the Go test runs through
// the server's applyMenuOp (backend/internal/admin/testdata/menu_ops_parity.json).
// Run: node --test scripts/admin-qa/unit/menulogic.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  blockEnd,
  clampDepths,
  dropTargets,
  duplicateBlock,
  indentBlock,
  moveBlock,
  moveToParent,
  outdentBlock,
  removeBlock,
  restoreBlock,
} from "../../../backend/web/admin/js/menulogic.js";

const cases = JSON.parse(readFileSync(new URL("../../../backend/internal/admin/testdata/menu_ops_parity.json", import.meta.url), "utf8"));
const toRows = (pairs) => pairs.map(([label, depth]) => ({ label, depth }));
const toPairs = (rows) => rows.map((r) => [r.label, r.depth]);

// Each server op maps to a client function; null (refused) leaves the rows alone.
function apply(c, rows) {
  const [name, idx] = c.op.split(".");
  const i = Number(idx);
  const out = {
    remove: () => removeBlock(rows, i)?.rows,
    dup: () => duplicateBlock(rows, i),
    outdent: () => outdentBlock(rows, i),
    indent: () => indentBlock(rows, i, c.max),
    up: () => moveBlock(rows, i, -1),
    down: () => moveBlock(rows, i, 1),
    moveto: () => {
      const t = c.target === "top" ? null : c.target === "" ? NaN : Number(c.target);
      return moveToParent(rows, i, t, c.max);
    },
  }[name]();
  return out ?? rows;
}

for (const c of cases) {
  test(`parity: ${c.name}`, () => {
    const rows = toRows(c.rows);
    const before = JSON.stringify(rows);
    assert.deepEqual(toPairs(apply(c, rows)), c.want);
    assert.equal(JSON.stringify(rows), before, "the input rows are not mutated");
  });
}

test("the fixture covers every server op", () => {
  const ops = new Set(cases.map((c) => c.op.split(".")[0]));
  for (const op of ["remove", "dup", "outdent", "indent", "up", "down", "moveto"]) assert.ok(ops.has(op), op);
});

test("blockEnd stops at the next row that is not deeper", () => {
  const rows = toRows([["A", 0], ["A1", 1], ["A11", 2], ["B", 0]]);
  assert.equal(blockEnd(rows, 0), 3);
  assert.equal(blockEnd(rows, 1), 3);
  assert.equal(blockEnd(rows, 3), 4);
});

test("restoreBlock undoes removeBlock with the same order and depths", () => {
  const rows = toRows([["A", 0], ["B", 0], ["B1", 1], ["B11", 2], ["C", 0]]);
  for (let i = 0; i < rows.length; i++) {
    const { rows: after, undo } = removeBlock(rows, i);
    assert.deepEqual(restoreBlock(after, undo), rows, `remove then restore ${i}`);
  }
});

test("outdent and indent are inverse for a last child", () => {
  const rows = toRows([["A", 0], ["A1", 1], ["A2", 1]]);
  assert.deepEqual(indentBlock(outdentBlock(rows, 2), 2, 5), rows);
});

test("operations refuse an index outside the list", () => {
  const rows = toRows([["A", 0]]);
  assert.equal(removeBlock(rows, 3), null);
  assert.equal(outdentBlock(rows, -1), null);
  assert.equal(indentBlock(rows, 1.5, 5), null);
  assert.equal(moveBlock(rows, 2, 1), null);
  assert.equal(duplicateBlock(rows, 1), null);
  assert.equal(moveToParent(rows, 0, 4, 5), null);
});

test("clampDepths matches the server's normaliseDepths", () => {
  const got = clampDepths([{ depth: 3 }, { depth: 5 }, { depth: 1 }, { depth: -2 }], 5);
  assert.deepEqual(got.map((r) => r.depth), [0, 1, 1, 0]);
  assert.deepEqual(clampDepths([{ depth: 0 }, { depth: 1 }, { depth: 2 }], 1).map((r) => r.depth), [0, 1, 1]);
});

test("dropTargets offers the valid depth nearest the pointer at each gap", () => {
  const rows = toRows([["A", 0], ["A1", 1], ["B", 0], ["C", 0]]);
  const slots = dropTargets(rows, 2, 1, 5); // drag B (no children) with the pointer one level in
  assert.deepEqual(slots.map((s) => [s.index, s.depth]), [[0, 0], [1, 1], [2, 1], [3, 1]]);
  assert.deepEqual(slots[1].label, { kind: "inside", index: 0 });
  assert.deepEqual(slots[0].label, { kind: "top" });
  assert.deepEqual(slots[3].label, { kind: "inside", index: 3 }, "after C, one level in, is under C");
  const flat = dropTargets(rows, 2, 0, 5);
  assert.ok(flat.every((s) => s.depth === 0));
  assert.deepEqual(flat[2].label, { kind: "top" });
});

test("dropTargets keeps a block with sub-items within max and valid", () => {
  const rows = toRows([["A", 0], ["B", 0], ["B1", 1]]);
  const slots = dropTargets(rows, 1, 9, 1); // B has one level under it, max 1 so B must stay top level
  assert.ok(slots.every((s) => s.depth === 0));
  assert.deepEqual(dropTargets(rows, 7, 0, 5), []);
});

test("dropTargets bounds the depth by the block's last row, so the label is the saved tree", () => {
  const rows = toRows([["A", 0], ["A1", 1], ["A11", 2], ["A111", 3], ["X", 0], ["X1", 1], ["X11", 2], ["X2", 1]]);
  const from = 4;
  const end = blockEnd(rows, from);
  for (const pointer of [0, 1, 2, 3]) {
    for (const slot of dropTargets(rows, from, pointer, 5)) {
      const rest = [...rows.slice(0, from), ...rows.slice(end)];
      const placed = [...rest.slice(0, slot.index), ...rows.slice(from, end).map((r) => ({ ...r, depth: r.depth - rows[from].depth + slot.depth })), ...rest.slice(slot.index)];
      assert.deepEqual(clampDepths(placed, 5), placed, `slot ${slot.index} depth ${slot.depth} is kept as shown`);
    }
  }
});
