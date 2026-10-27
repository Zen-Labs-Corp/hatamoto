// pickSelection: plain, Ctrl/Cmd toggle and Shift sibling range. Run: node --test scripts/admin-qa/unit/builder-selection.test.mjs
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
const { pickSelection } = await import(new URL("../../../backend/web/admin/js/builder/clipops.js", import.meta.url));

const leaf = (id) => ({ id, t: "text", p: { text: id } });
const box = (id, c = []) => ({ id, t: "container", ...(c.length ? { c } : {}) });
const doc = { v: 2, nodes: [box("a", [leaf("a1"), leaf("a2"), leaf("a3"), leaf("a4")]), box("b", [leaf("b1")]), box("c")] };
const ids = (r) => [...r.selection];

test("a plain click selects only that element and anchors there", () => {
  const r = pickSelection(doc, new Set(["a1", "a2"]), "a1", "b1", {});
  assert.deepEqual(ids(r), ["b1"]);
  assert.equal(r.primary, "b1");
  assert.equal(r.anchor, "b1");
});

test("Ctrl toggles: adds, makes the new one primary, and removing falls back to the last remaining", () => {
  let r = pickSelection(doc, new Set(["a1"]), "a1", "a3", { ctrl: true });
  assert.deepEqual(ids(r), ["a1", "a3"]);
  assert.equal(r.primary, "a3");
  r = pickSelection(doc, r.selection, r.anchor, "a3", { ctrl: true });
  assert.deepEqual(ids(r), ["a1"]);
  assert.equal(r.primary, "a1");
  r = pickSelection(doc, r.selection, r.anchor, "a1", { ctrl: true });
  assert.deepEqual(ids(r), []);
  assert.equal(r.primary, null);
  assert.equal(r.anchor, null);
});

test("Ctrl may pick across parents", () => {
  const r = pickSelection(doc, new Set(["a1"]), "a1", "b1", { ctrl: true });
  assert.deepEqual(ids(r), ["a1", "b1"]);
});

test("Shift selects the sibling range from the anchor, either direction, keeping the anchor", () => {
  let r = pickSelection(doc, new Set(["a2"]), "a2", "a4", { shift: true });
  assert.deepEqual(ids(r), ["a2", "a3", "a4"]);
  assert.equal(r.primary, "a4");
  assert.equal(r.anchor, "a2");
  r = pickSelection(doc, r.selection, r.anchor, "a1", { shift: true });
  assert.deepEqual(ids(r), ["a1", "a2"]);
});

test("Shift with an anchor under another parent, or none, is an ordinary click", () => {
  assert.deepEqual(ids(pickSelection(doc, new Set(["a1"]), "a1", "b1", { shift: true })), ["b1"]);
  assert.deepEqual(ids(pickSelection(doc, new Set(), null, "a3", { shift: true })), ["a3"]);
});

test("Ctrl+Shift adds the range to the selection", () => {
  const r = pickSelection(doc, new Set(["b", "a1"]), "a1", "a3", { shift: true, ctrl: true });
  assert.deepEqual(ids(r).sort(), ["a1", "a2", "a3", "b"]);
});

test("an unknown id clears the selection", () => {
  const r = pickSelection(doc, new Set(["a1"]), "a1", "gone", {});
  assert.equal(r.selection.size, 0);
  assert.equal(r.primary, null);
});
