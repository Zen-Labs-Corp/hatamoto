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
