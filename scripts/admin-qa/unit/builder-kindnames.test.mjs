// The one label map: boot labels win, fixes cover compound kinds, the rest is title-cased.
// Run: node --test scripts/admin-qa/unit/builder-kindnames.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { makeKindNames, refName, SEP } = await import(new URL("../../../backend/web/admin/js/builder/kindnames.js", import.meta.url));

test("boot label wins over the fallback", () => {
  const name = makeKindNames([{ kind: "richtext", label: "Rich text block" }, { kind: "heading" }]);
  assert.equal(name("richtext"), "Rich text block");
  assert.equal(name("heading"), "Heading");
});

test("compound kinds and unknown kinds read plainly", () => {
  const name = makeKindNames();
  assert.equal(name("accorditem"), "Accordion item");
  assert.equal(name("tabpanel"), "Tab panel");
  assert.equal(name("spacer"), "Spacer");
});

test("ref names and separator", () => {
  assert.equal(refName("Footer"), "Block: Footer");
  assert.equal(SEP, " · ");
});
