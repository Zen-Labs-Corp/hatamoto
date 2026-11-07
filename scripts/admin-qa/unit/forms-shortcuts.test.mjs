// The inspector's shortcut key caps. Run: node --test scripts/admin-qa/unit/forms-shortcuts.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { KEYS, keyCap } = await import(new URL("../../../backend/web/admin/js/forms/shortcuts.js", import.meta.url));

test("a Mac gets symbols, modifiers first, no separator", () => {
  assert.equal(keyCap(["Mod", "S"], true), "⌘S");
  assert.equal(keyCap(["Mod", "Shift", "Z"], true), "⇧⌘Z");
  assert.equal(keyCap(["Alt", "Up"], true), "⌥↑");
  assert.equal(keyCap(["Delete"], true), "Delete");
});

test("other systems get Ctrl and Alt spelled out", () => {
  assert.equal(keyCap(["Mod", "S"], false), "Ctrl+S");
  assert.equal(keyCap(["Mod", "Shift", "Z"], false), "Ctrl+Shift+Z");
  assert.equal(keyCap(["Alt", "Down"], false), "Alt+↓");
  assert.equal(keyCap(["Esc"], false), "Esc");
});

test("every row has a key and a meaning", () => {
  for (const [combos, what] of KEYS) assert.ok(combos.length && combos.every((c) => c.length) && what);
});
