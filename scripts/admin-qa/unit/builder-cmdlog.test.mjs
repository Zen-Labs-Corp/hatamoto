// The generic command log. Run: node --test scripts/admin-qa/unit/builder-cmdlog.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const L = await import(new URL("../../../backend/web/admin/js/builder/cmdlog.js", import.meta.url));

// A toy document: a flat object; op {k, v} sets a key and its inverse restores the old value.
const apply = (doc, op) => ({ doc: { ...doc, [op.k]: op.v }, inv: { k: op.k, v: doc[op.k] } });
const log = L.commitLog({ apply, coalesceKey: (ops) => (ops.length === 1 ? ops[0].k : null) });
const set = (k, v) => [{ k, v }];
const T0 = 1_000_000;

test("commit, undo, redo", () => {
  let s = log.createStore({});
  assert.equal(log.canUndo(s), false);
  s = log.commit(s, set("a", 1), { now: T0 });
  s = log.commit(s, set("b", 2), { now: T0 + 5000 });
  assert.deepEqual(s.doc, { a: 1, b: 2 });
  assert.equal(log.canUndo(s), true);
  s = log.undo(s);
  assert.deepEqual(s.doc, { a: 1, b: undefined });
  assert.equal(log.canRedo(s), true);
  s = log.redo(s);
  assert.equal(s.doc.b, 2);
  assert.equal(log.canRedo(s), false);
  assert.deepEqual(log.undo(log.createStore({})).applied, []);
});

test("jump moves several steps and clamps", () => {
  let s = log.createStore({});
  for (let i = 1; i <= 3; i++) s = log.commit(s, set("k" + i, i), { now: T0 + i * 5000 });
  s = log.jump(s, -2);
  assert.equal(s.past.length, 1);
  assert.equal(s.applied.length, 2);
  s = log.jump(s, 10);
  assert.equal(s.future.length, 0);
  assert.equal(s.doc.k3, 3);
  assert.equal(log.jump(s, 0).applied.length, 0);
});

test("coalesce merges a lone op inside one second, keeping the first inverse", () => {
  let s = log.createStore({ a: 0 });
  s = log.commit(s, set("a", 1), { coalesce: true, now: T0 });
  s = log.commit(s, set("a", 2), { coalesce: true, now: T0 + 500 });
  assert.equal(s.past.length, 1);
  s = log.commit(s, set("a", 3), { coalesce: true, now: T0 + 5000 });
  assert.equal(s.past.length, 2);
  s = log.commit(s, set("b", 1), { coalesce: true, now: T0 + 5100 });
  assert.equal(s.past.length, 3);
  s = log.jump(s, -2);
  assert.equal(s.doc.a, 2); // the 3rd entry undone, then b; the merged 1+2 entry stays
  s = log.undo(s);
  assert.equal(s.doc.a, 0); // the merged entry restores the first inverse
});

test("a string session key merges however long the pause", () => {
  let s = log.createStore({});
  s = log.commit(s, set("a", 1), { coalesce: "drag", now: T0 });
  s = log.commit(s, set("a", 2), { coalesce: "drag", now: T0 + 60_000 });
  s = log.commit(s, set("a", 3), { coalesce: "drag", now: T0 + 120_000 });
  assert.equal(s.past.length, 1);
  assert.equal(s.doc.a, 3);
  s = log.undo(s);
  assert.equal(s.doc.a, undefined);
  s = log.redo(s);
  assert.equal(s.doc.a, 3);
});

test("without a coalesceKey the timer never merges; history is capped", () => {
  const plain = L.commitLog({ apply });
  let s = plain.createStore({});
  s = plain.commit(s, set("a", 1), { coalesce: true, now: T0 });
  s = plain.commit(s, set("a", 2), { coalesce: true, now: T0 + 1 });
  assert.equal(s.past.length, 2);
  for (let i = 0; i < L.MAX_HISTORY + 20; i++) s = plain.commit(s, set("x", i), { now: T0 + i });
  assert.equal(s.past.length, L.MAX_HISTORY);
});
