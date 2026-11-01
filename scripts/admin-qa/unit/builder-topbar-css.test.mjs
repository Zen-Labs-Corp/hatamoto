// Source guards for the builder top bar's media queries (a layout bug here only shows at one exact width).
// Run: node --test scripts/admin-qa/unit/builder-topbar-css.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builder.css", import.meta.url), "utf8");

test("the grid bar and the phone bar never match at the same width (640px belongs to the phone)", () => {
  assert.match(css, /@media \(max-width: 40rem\) \{/);
  assert.doesNotMatch(css, /@media \(min-width: 40rem\)/, "an inclusive min-width overlaps the phone rules at exactly 640px");
  assert.match(css, /@media not all and \(max-width: 40rem\) \{[^\n]*\n\s*\.hm-editor-bar:has\(\[data-hm-bs-title\]\) \{ display: grid;/);
});

test("forced colors keep the Update dot and the hint's resize glyph (both are backgrounds)", () => {
  const i = css.indexOf("@media (forced-colors: active) { /* a background");
  assert.ok(i > 0, "no forced-colors block for the dot and the glyph");
  const block = css.slice(i, css.indexOf("\n}\n", i));
  assert.match(block, /\[data-hm-bs-dirty\]::after \{ forced-color-adjust: none; background: ButtonText;/);
  assert.match(block, /\.hm-bs-hint__bar \{ forced-color-adjust: none; background: CanvasText;/);
  assert.doesNotMatch(css, /so it also works in forced colours/);
});
