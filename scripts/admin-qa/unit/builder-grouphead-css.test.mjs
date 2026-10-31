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
