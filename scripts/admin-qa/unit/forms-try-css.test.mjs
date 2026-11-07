// Source guard: Try it hides the "Add a field" disclosure with the palette, and closes the palette on entry.
// Run: node --test scripts/admin-qa/unit/forms-try-css.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../../../backend/web/admin/${p}`, import.meta.url), "utf8");

test("Try it hides the Add a field toggle with the palette and inspector", () => {
  assert.match(read("css/pages/formsdesigner.css"), /\.hm-fd\[data-mode="try"\] :is\(\.hm-fd__add, \.hm-fd__palette, \.hm-fd__inspector\) \{ display: none; \}/);
});

test("entering Try it closes the palette", () => {
  const js = read("js/forms/screen.js");
  const i = js.indexOf("select(null); // nothing stays selected");
  assert.ok(i > 0);
  assert.match(js.slice(i, i + 200), /setAdd\(false\)/);
});
