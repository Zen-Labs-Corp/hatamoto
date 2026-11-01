// The one status chip text: "Draft saved · not live yet", "Published · Saved", "Saving…", "Not saved", "Changed somewhere else".
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
const { stateLine, shortLine, savedTime, updateState } = await import(new URL("../../../backend/web/admin/js/builder/sync.js", import.meta.url));

test("saved: a draft says it is not live, a published page says Published, a block or part just Saved", () => {
  assert.equal(stateLine({ state: "saved" }, { status: "Draft" }), "Draft saved · not live yet");
  assert.equal(stateLine({ state: "saved" }, { status: "Published" }), "Published · Saved");
  assert.equal(stateLine({ state: "saved" }), "Saved");
});

test("the time is not in the line: a later save does not change the text", () => {
  assert.equal(stateLine({ state: "saved" }, { status: "Published", at: "14:02" }), "Published · Saved");
});

test("savedTime is the title text, empty before the first save", () => {
  assert.equal(savedTime("14:02"), "Saved at 14:02");
  assert.equal(savedTime(""), "");
  assert.equal(savedTime(), "");
});

test("working states leave the status out", () => {
  assert.equal(stateLine({ state: "saving" }, { status: "Draft" }), "Saving…");
  assert.equal(stateLine({ state: "saving" }, { status: "Published" }), "Saving…");
  assert.equal(stateLine({ state: "conflict" }, { status: "Draft" }), "Changed somewhere else");
  assert.equal(stateLine({ state: "stopped", message: "Stopped." }), "Stopped.");
  assert.equal(stateLine({ state: "stopped" }), "Saving stopped");
  assert.equal(stateLine({ state: "error", message: "Offline." }), "Offline. Retrying…");
  assert.equal(stateLine({ state: "error" }), "The page could not be saved. Retrying…");
  assert.equal(stateLine({ state: "idle" }), "");
});

test("pending: a draft page autosaves (Not saved); a published page keeps the edits as a draft", () => {
  assert.equal(stateLine({ state: "pending" }, { status: "Draft", draft: true }), "Not saved");
  assert.equal(stateLine({ state: "pending", keep: "kept" }, { status: "Published" }), "Draft saved · not live yet");
  assert.equal(stateLine({ state: "pending" }, { status: "Published" }), "Unsaved changes");
});

test("the failure lines of a published page are unchanged", () => {
  const line = (keep) => stateLine({ state: "pending", keep }, { status: "Published" });
  assert.equal(line("failed"), "Not saved. You appear to be offline.");
  assert.equal(line("expired"), "Your session ended: sign in again in another tab.");
  assert.equal(line("refused"), "Unsaved changes, not kept. Update the page to save them.");
});

test("shortLine: the few words a phone keeps, never a reason", () => {
  assert.equal(shortLine({ state: "saved" }, "Draft"), "Draft · Saved");
  assert.equal(shortLine({ state: "saved" }), "Saved");
  assert.equal(shortLine({ state: "pending" }), "Not saved");
  assert.equal(shortLine({ state: "saving" }), "Saving…");
  assert.equal(shortLine({ state: "error", message: "You appear to be offline." }), "Not saved");
  assert.equal(shortLine({ state: "conflict" }), "Changed elsewhere");
  assert.equal(shortLine({ state: "stopped" }), "Not saved");
  assert.equal(shortLine({ state: "idle" }), "");
});

test("updateState: only a published page with edits not yet live shows the indicator, and it clears on saved", () => {
  const u = (q, o) => updateState(q, o).unpublished;
  assert.equal(u({ state: "pending", keep: "kept" }, { status: "Published" }), true);
  assert.equal(u({ state: "pending" }, { status: "Published", dirty: true }), true);
  assert.equal(u({ state: "saving" }, { status: "Published", dirty: true }), true);
  assert.equal(u({ state: "saving", keep: "kept" }, { status: "Published" }), true);
  assert.equal(u({ state: "error", keep: "failed" }, { status: "Published", dirty: true }), true); // the edits are still unsaved
  assert.equal(u({ state: "pending" }, { status: "Published" }), false); // nothing changed
  assert.equal(u({ state: "saved" }, { status: "Published", dirty: true }), false);
  assert.equal(u({ state: "saved", keep: "kept" }, { status: "Published" }), false);
  assert.equal(u({ state: "idle" }, { status: "Published", dirty: true }), false);
});

test("updateState: a draft page, a block and a part never show it", () => {
  for (const state of ["pending", "saving", "error", "conflict", "stopped", "saved"]) {
    assert.equal(updateState({ state, keep: "kept" }, { status: "Draft", dirty: true }).unpublished, false, state);
    assert.equal(updateState({ state, keep: "kept" }, { status: "", dirty: true }).unpublished, false, state);
  }
});
