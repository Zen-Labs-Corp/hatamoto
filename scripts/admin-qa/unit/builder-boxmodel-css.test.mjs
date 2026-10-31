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

test("the selected choice option has a semibold label (ADMIN_UI_RULES rule 6)", () => {
  const rule = /^\.hm-bs-style \.hm-bs-opt\[aria-pressed="true"\][^{]*\{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rule, /font-weight:\s*var\(--weight-semibold\)/);
});

test("the ring caption has its own band above the top number and the size text wraps instead of overflowing", () => {
  const rings = /^\.hm-bs-bm__m, \.hm-bs-bm__p \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rings, /grid-template-areas:\s*"cap cap cap"/, "the caption has its own grid row");
  assert.doesNotMatch(/^\.hm-bs-bm__t \{([^}]*)\}/m.exec(css)?.[1] ?? "", /position:\s*absolute/);
  const el = /^\.hm-bs-bm__el \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.doesNotMatch(el, /white-space:\s*nowrap/);
  assert.match(el, /overflow:\s*hidden/);
  assert.match(css, /^\.hm-bs-bm__c:has\(> \.hm-bs-bm__el\) \{[^}]*overflow:\s*hidden/m, "what does not fit is clipped, never painted over the side numbers");
});
