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

test("no focus ring after a mouse click, still the paper ring on a selected row with the keyboard", () => {
  assert.match(css, /\[data-pointer\] > \.hm-bs-tree \.hm-bs-row:focus-visible[^{]*\{ outline: none; \}/);
  assert.match(rule('.hm-bs-row[aria-selected="true"]:focus-visible'), /outline: 2px solid var\(--bs-nav-on-accent\)/);
});

test("the no-ring-after-click rule is off in forced-colors, where the outline is the only selected cue", () => {
  const at = css.indexOf("@media not (forced-colors: active)");
  const hit = css.indexOf("[data-pointer] > .hm-bs-tree .hm-bs-row:focus-visible");
  assert.ok(at >= 0 && hit > at, "rule must sit inside @media not (forced-colors: active)");
  assert.ok(css.indexOf("}\n", hit) < css.indexOf("@media (forced-colors: active) { .hm-bs-row[aria-selected", hit));
  assert.match(css, /@media \(forced-colors: active\) \{ \.hm-bs-row\[aria-selected="true"\] \{ outline: 2px solid Highlight/);
});

test("chevron and Collapse are quiet: muted thin chevron, muted regular-weight Collapse", () => {
  assert.match(rule(".hm-bs-row__caret"), /color: var\(--text-muted\)/);
  assert.match(rule(".hm-bs-row__caret .hm-icon"), /stroke-width: 1\.5/);
  assert.doesNotMatch(css, /\\25B[8E]/); // the filled triangles are gone
  const c = rule(".hm-bs__collapse");
  assert.match(c, /color: var\(--text-muted\)/);
  assert.match(c, /font-weight: var\(--weight-regular\)/);
});

test("the Add section footer is a plain dashed button with a focus ring and a forced-colors edge", () => {
  const b = rule('.hm-bs__navfoot [data-hm-bs-addbtn]');
  assert.match(b, /border: 1px dashed var\(--border-control\)/);
  assert.match(b, /min-block-size: 24px/);
  assert.match(css, /\[data-hm-bs-addbtn\]:focus-visible \{ outline: var\(--focus-ring\)/);
  assert.match(css, /forced-colors: active\) \{ \.hm-bs__navfoot \[data-hm-bs-addbtn\] \{ border-color: ButtonText/);
});
