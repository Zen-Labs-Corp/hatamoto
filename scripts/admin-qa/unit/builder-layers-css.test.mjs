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

test("the add-here pill is no taller than the gap, so it never covers the band above", () => {
  assert.match(rule(".hm-bs-gap__btn"), /block-size: var\(--space-bs-gap\)/);
  assert.match(rule(".hm-bs-gap"), /block-size: var\(--space-bs-gap\)/);
});

test("the Pages tab: a muted page icon, the path small and muted, the current page solid", () => {
  assert.match(rule(".hm-bs-page__ico"), /color: var\(--text-muted\)/);
  assert.match(rule(".hm-bs-page__path"), /font-size: var\(--text-xs\)[^}]*color: var\(--text-muted\)/);
  assert.match(rule('.hm-bs-page[aria-current="page"], .hm-bs-page[aria-current="page"]:hover'), /background: var\(--bs-nav-accent/);
});

test("the active left tab is a paper pill with a hairline ring, like a selected segment", () => {
  const r = rule('.hm-bs__ltab[aria-selected="true"]');
  assert.match(r, /background-color: var\(--color-paper\)/);
  assert.match(r, /box-shadow: 0 0 0 1px var\(--segment-ring\)/);
  assert.doesNotMatch(r, /surface-hover/);
});
