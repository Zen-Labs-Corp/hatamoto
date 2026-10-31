// The box model numbers must be wide enough for a value like "1.25rem" (7 characters at the small text size).
// Run: node --test scripts/admin-qa/unit/builder-boxmodel-css.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builderinspector.css", import.meta.url), "utf8");

test("a box model number is at least 3.5rem wide", () => {
  const rule = /^\.hm-bs-bm__n \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  const w = /inline-size:\s*([\d.]+)rem/.exec(rule);
  assert.ok(w, "the number sets an inline-size in rem");
  assert.ok(Number(w[1]) >= 3.5, `inline-size ${w[1]}rem cannot show 1.25rem`);
});

test("forced colours: the numbers keep a visible edge and a refused one changes width and style", () => {
  const block = /@media \(forced-colors: active\) \{([\s\S]*?)\n\}/.exec(css)?.[1] ?? "";
  assert.match(block, /\.hm-bs-bm__n \{[^}]*border-color:\s*ButtonBorder/);
  assert.match(block, /\.hm-bs-bm__n\[aria-invalid="true"\] \{[^}]*border-width:\s*2px/);
});

test("an inherited choice option keeps a dashed border in the segmented track (forced colours draw borders, not shadows)", () => {
  const rule = /^\.hm-bs-style \.hm-bs-opt--inherited \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rule, /border:\s*1px dashed var\(--color-hai\)/);
  assert.doesNotMatch(rule, /border:\s*0/);
});
