// Radio menu items (the builder's Fit menu) take part in the shared menu keyboard handling.
// Run: node --test scripts/admin-qa/unit/menu-radio-items.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/components.js", import.meta.url), "utf8");

test("menuItems collects menuitemradio as well as menuitem", () => {
  const line = src.split("\n").find((l) => l.startsWith("const menuItems"));
  assert.ok(line, "menuItems is defined");
  assert.match(line, /role="menuitemradio"/);
});
