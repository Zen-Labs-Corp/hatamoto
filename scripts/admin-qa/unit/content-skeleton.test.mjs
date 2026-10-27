// The media picker shows a skeleton grid with a status line while the library loads.
// Run: node --test scripts/admin-qa/unit/content-skeleton.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../../../backend/web/admin/${p}`, import.meta.url), "utf8");

test("the picker opens on a status region saying Loading images…, not a bare word", () => {
  const js = read("js/content.js");
  const open = js.slice(js.indexOf("async function openPicker"), js.indexOf('document.addEventListener("click"'));
  assert.match(open, /hm-media-skeleton" role="status"/);
  assert.match(open, /Loading images…/);
  assert.doesNotMatch(open, /textContent = "Loading…"/);
});

test("the skeleton is styled and rests under reduced motion", () => {
  const css = read("css/pages/content.css");
  assert.match(css, /\.hm-media-skeleton > i \{[^}]*hm-chart-pulse/);
  assert.match(css, /prefers-reduced-motion: reduce\) \{ \.hm-media-skeleton > i \{ animation: none/);
});
