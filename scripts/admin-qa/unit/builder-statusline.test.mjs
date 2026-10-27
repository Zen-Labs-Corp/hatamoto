// The one status chip text: "Draft · Saved 14:02", "Saving…", "Not saved", "Changed somewhere else".
// Run: node --test scripts/admin-qa/unit/builder-statusline.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { stateLine, shortLine } = await import(new URL("../../../backend/web/admin/js/builder/sync.js", import.meta.url));

test("saved carries the status and the time", () => {
  assert.equal(stateLine({ state: "saved" }, { status: "Draft", at: "14:02" }), "Draft · Saved 14:02");
  assert.equal(stateLine({ state: "saved" }, { status: "Published", at: "14:02" }), "Published · Saved 14:02");
  assert.equal(stateLine({ state: "saved" }, { status: "Draft" }), "Draft · Saved");
});

test("a block or part has no status: just Saved <time>", () => {
  assert.equal(stateLine({ state: "saved" }, { at: "14:02" }), "Saved 14:02");
});

test("working states leave the status out", () => {
  assert.equal(stateLine({ state: "saving" }, { status: "Draft" }), "Saving…");
  assert.equal(stateLine({ state: "pending" }, { status: "Draft", draft: true }), "Not saved");
  assert.equal(stateLine({ state: "conflict" }, { status: "Draft" }), "Changed somewhere else");
});

test("shortLine: the few words a phone keeps, never the time or a reason", () => {
  assert.equal(shortLine({ state: "saved" }, "Draft"), "Draft · Saved");
  assert.equal(shortLine({ state: "saved" }), "Saved");
  assert.equal(shortLine({ state: "pending" }), "Not saved");
  assert.equal(shortLine({ state: "error", message: "You appear to be offline." }), "Not saved");
  assert.equal(shortLine({ state: "conflict" }), "Changed elsewhere");
  assert.equal(shortLine({ state: "idle" }), "");
});
