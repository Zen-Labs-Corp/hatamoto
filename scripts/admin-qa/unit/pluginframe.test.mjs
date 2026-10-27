// pluginframe.js: a frame that loads a second time is blanked, never reloaded
// from its (spent, one-time) ticket URL. Run: node --test scripts/admin-qa/unit/pluginframe.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/pluginframe.js", import.meta.url), "utf8");

test("a second load blanks the frame and shows the notice", () => {
  const code = src.replace(/\/\/.*$/gm, "");
  assert.equal((code.match(/frame\.src\s*=/g) || []).length, 2, "the first load and the blanking, nothing else");
  assert.match(code, /dead = true;\s*frame\.src = "about:blank";\s*if \(notice\) notice\.hidden = false;/);
  assert.doesNotMatch(code, /frame\.src\s*=\s*src\s*;[\s\S]*frame\.src\s*=\s*src\s*;/, "the ticket URL is set once");
});
