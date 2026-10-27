// Renumbering of builder rows and menu items, menu undo, and focus after a file Remove.
// Run: node --test scripts/admin-qa/unit/listlogic.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { removeDepths, removeFocusIndex, renumberOp, renumberRef, restoreDepths } from "../../../backend/web/admin/js/listlogic.js";

test("renumberRef rewrites names, ids and id-ref lists for the row's new place", () => {
  assert.equal(renumberRef("f.2.label", "f", 0), "f.0.label");
  assert.equal(renumberRef("f-2-label", "f", 0), "f-0-label");
  assert.equal(renumberRef("f-2-label-error", "f", 1), "f-1-label-error");
  assert.equal(renumberRef("hint f-2-label-error", "f", 1), "hint f-1-label-error");
  assert.equal(renumberRef("f-2-a f-2-b", "f", 5), "f-5-a f-5-b");
  assert.equal(renumberRef("i.3.url", "f", 0), "i.3.url", "another prefix is left alone");
  assert.equal(renumberRef("title", "f", 0), "title");
});

test("dragging row 2 to the top gives three distinct, consecutive names", () => {
  const rows = [["f.0.name", "f-0-name"], ["f.1.name", "f-1-name"], ["f.2.name", "f-2-name"]];
  const dom = [rows[2], rows[0], rows[1]];
  const got = dom.map(([n, id], i) => [renumberRef(n, "f", i), renumberRef(id, "f", i)]);
  assert.deepEqual(got, [["f.0.name", "f-0-name"], ["f.1.name", "f-1-name"], ["f.2.name", "f-2-name"]]);
  assert.equal(renumberOp("up.2", 0), "up.0");
  assert.equal(renumberOp("remove.1", 2), "remove.2");
});

test("remove promotes the sub-items one level", () => {
  assert.deepEqual(removeDepths([0, 1, 2, 1, 0], 0), { depths: [0, 1, 0, 0], promoted: 3 });
  assert.deepEqual(removeDepths([0, 1, 0], 1), { depths: [0, 0], promoted: 0 });
  assert.deepEqual(removeDepths([0, 0, 1], 0), { depths: [0, 1], promoted: 0 });
});

test("undo after remove restores the tree it removed from", () => {
  for (const [depths, idx] of [[[0, 1, 2, 1, 0], 0], [[0, 1, 2, 1, 0], 1], [[0, 0, 1, 1, 0], 1]]) {
    const { depths: rest, promoted } = removeDepths(depths, idx);
    assert.deepEqual(restoreDepths(rest, idx, depths[idx], promoted), depths);
  }
});

test("undo after the promoted children were moved off does not re-indent the wrong items", () => {
  const { depths: rest, promoted } = removeDepths([1, 2, 2, 0], 0);
  assert.deepEqual([rest, promoted], [[1, 1, 0], 2]);
  // The user outdented both children after the remove: they are above the item's depth now.
  assert.deepEqual(restoreDepths([0, 0, 0], 0, 1, promoted), [1, 0, 0, 0]);
  // Nothing was promoted: only the item itself comes back.
  assert.deepEqual(restoreDepths([0, 1, 0], 1, 1, 0), [0, 1, 1, 0]);
});

test("focus after a file Remove goes to the same place, then the one before, else the input", () => {
  assert.equal(removeFocusIndex(1, 3), 1);
  assert.equal(removeFocusIndex(2, 2), 1);
  assert.equal(removeFocusIndex(0, 0), -1);
});
