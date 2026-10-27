// API key form: Select all ticks every listed scope but never "*" (Everything / full access).
// Run: node --test scripts/admin-qa/unit/apikeys.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/apikeys.js", import.meta.url), "utf8");
let n = 0;
// A data: URL with a unique tail is a fresh module each time, so the script runs again.
const load = () => import(`data:text/javascript,${encodeURIComponent(src)}%0A//${++n}`);

function box(value) {
  return { value, checked: false };
}

function setup() {
  const boxes = [box("pages:read"), box("pages:write"), box("*")];
  const text = { textContent: "Select all" };
  const listeners = {};
  const btn = {
    hidden: true,
    querySelector: () => text,
    addEventListener: (type, fn) => { listeners[type] = fn; },
  };
  const fsListeners = {};
  const fs = {
    querySelector: (sel) => (sel === "[data-hm-scopes-all]" ? btn : null),
    querySelectorAll: (sel) => {
      assert.match(sel, /:not\(\[value="\*"\]\)/, "selector must exclude the * scope");
      return boxes.filter((b) => b.value !== "*");
    },
    addEventListener: (type, fn) => { fsListeners[type] = fn; },
  };
  globalThis.document = { querySelectorAll: () => [fs] };
  return { boxes, text, btn, click: () => listeners.click(), change: () => fsListeners.change() };
}

test("Select all skips the * scope and toggles to Clear all and back", async () => {
  const s = setup();
  await load();
  assert.equal(s.btn.hidden, false);
  s.click();
  assert.deepEqual(s.boxes.map((b) => b.checked), [true, true, false]);
  assert.equal(s.text.textContent, "Clear all");
  s.click();
  assert.deepEqual(s.boxes.map((b) => b.checked), [false, false, false]);
  assert.equal(s.text.textContent, "Select all");
});

test("ticking * by hand does not change the button label", async () => {
  const s = setup();
  await load();
  assert.equal(s.btn.hidden, false);
  s.boxes[2].checked = true;
  s.change();
  assert.equal(s.text.textContent, "Select all");
});
