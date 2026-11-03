// A group header's +/- glyph is decoration: it must carry an empty alt text so it is not part of the button's name.
// Run: node --test scripts/admin-qa/unit/builder-grouphead-css.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builder.css", import.meta.url), "utf8");

test("the open/closed glyph of a group header has an empty alternative text", () => {
  const rules = css.split("\n").filter((l) => /^button\.hm-bs-grp__t(\[aria-expanded="true"\])?::after \{/.test(l));
  assert.equal(rules.length, 2);
  for (const r of rules) assert.match(r, /content:\s*"[^"]+"\s*\/\s*"";/);
});

test("the inspector pane has no padding of its own: tabs, header and groups are full-bleed rows that own their inset", () => {
  assert.match(css, /\.hm-bs__nav, \.hm-bs__inspector \{ padding: 0;/);
  assert.match(css, /^\.hm-bs-grp \{ border-block-end: 1px solid var\(--border\); padding: var\(--space-1\) var\(--space-3\) var\(--space-3\);/m);
  assert.match(css, /^\.hm-bs__inspector \.hm-bs-sections \{ margin: 0; padding-inline: var\(--space-2\); border-block-end: 1px solid var\(--border\); \}/m);
  assert.match(css, /^\.hm-bs__inspector \.hm-bs-header \{[^}]*border-block-end: 1px solid var\(--border\);/m);
  assert.doesNotMatch(css, /\.hm-bs__device[ [:{]/, "the top-bar device chip rules are gone");
});

test("the Advanced tab uses the Design tab's type scale: group titles and field text are text-xs", () => {
  const own = readFileSync(new URL("../../../backend/web/admin/css/pages/builderinspector.css", import.meta.url), "utf8");
  assert.match(own, /^\.hm-bs-advanced, \.hm-bs-advanced :is\([^)]*\.hm-field__control[^)]*\) \{ font-size: var\(--text-xs\);/m);
  assert.match(own, /^\.hm-bs-advanced \.hm-bs-grp__hd[^{]*\{[^}]*font-size: var\(--text-xs\);/m);
});
