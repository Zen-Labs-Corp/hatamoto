// The Layers panel's look, pinned in builder.css (the visual acceptance findings against the mockup).
// Run: node --test scripts/admin-qa/unit/builder-layers-css.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builder.css", import.meta.url), "utf8");
/** The declarations of every single-line rule whose selector list is exactly `sel`, joined. */
const rule = (sel) =>
  css
    .split("\n")
    .filter((l) => l.startsWith(`${sel} {`))
    .map((l) => l.slice(l.indexOf("{") + 1, l.lastIndexOf("}")))
    .join(" ");

test("sections sit 56px apart: the 44px row plus a 12px gap", () => {
  assert.match(rule(".hm-bs-tree"), /--bs-nav-section-h: 44px/);
  assert.match(rule(".hm-bs-tree"), /--space-bs-gap: var\(--space-3\)/); // the margin between sections and the height of the add-here gap
});
