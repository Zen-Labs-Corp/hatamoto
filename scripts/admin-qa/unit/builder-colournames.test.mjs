// The shared colour names: Brand, Background, Text, Surface, Muted, Accent (the Go map mirrors them; TestColourNamesMirrorJS).
// Run: node --test scripts/admin-qa/unit/builder-colournames.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { COLOUR_NAME } = await import(new URL("../../../backend/web/admin/js/builder/colournames.js", import.meta.url));

test("every builder colour has one plain name", () => {
  assert.deepEqual(COLOUR_NAME, { primary: "Brand", bg: "Background", text: "Text", surface: "Surface", muted: "Muted", accent: "Accent" });
});

test("no internal terms or em dashes in the names", () => {
  for (const n of Object.values(COLOUR_NAME)) assert.doesNotMatch(n, /token|node|slot|preset|—/i);
});
