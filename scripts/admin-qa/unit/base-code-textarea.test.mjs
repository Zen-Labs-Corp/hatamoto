// Source guard: only code textareas (marked data-hm-code) are monospace, not every readonly textarea.
// Run: node --test scripts/admin-qa/unit/base-code-textarea.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../../../backend/web/admin/${p}`, import.meta.url), "utf8");

test("the monospace rule is scoped to data-hm-code, not every readonly textarea", () => {
  const css = read("css/base.css");
  assert.doesNotMatch(css, /textarea\[readonly\]/);
  assert.match(css, /^textarea\[data-hm-code\] \{ font-family: var\(--font-mono\)/m);
});

test("the embed code textarea carries the marker", () => {
  assert.match(read("pages/forms/share.html"), /"Control" "textarea"[^\n]*"data-hm-code" ""/);
});
