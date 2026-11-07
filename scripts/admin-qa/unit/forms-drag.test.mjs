// The designer's drop position and palette items. Run: node --test scripts/admin-qa/unit/forms-drag.test.mjs
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
const { dropIndex } = await import(new URL("forms/drag.js", root));
const { paletteItems } = await import(new URL("forms/palette.js", root));
const { filterItems } = await import(new URL("builder/search.js", root));

const rects = [0, 100, 200].map((top) => ({ top, height: 80 })); // midpoints 40, 140, 240

test("dropIndex: top, between, end and empty", () => {
  assert.equal(dropIndex(-20, rects), 0);
  assert.equal(dropIndex(39, rects), 0);
  assert.equal(dropIndex(41, rects), 1);
  assert.equal(dropIndex(100, rects), 1);
  assert.equal(dropIndex(141, rects), 2);
  assert.equal(dropIndex(241, rects), 3);
  assert.equal(dropIndex(900, rects), 3);
  assert.equal(dropIndex(50, []), 0);
});

test("the palette lists the known types in group order and leaves out file", () => {
  const items = paletteItems([{ type: "file" }, { type: "consent" }, { type: "email" }, { type: "select" }, { type: "divider" }]);
  assert.deepEqual(items.map((i) => i.key), ["email", "select", "consent", "divider"]);
});

test("the palette filter matches the label", () => {
  const items = paletteItems([{ type: "email" }, { type: "text" }, { type: "textarea" }]);
  assert.deepEqual(filterItems(items, "long").map((i) => i.key), ["textarea"]);
  assert.equal(filterItems(items, "zzz").length, 0);
  assert.equal(filterItems(items, "").length, 3);
});
