// The inline SEO view's pure logic. Run: node --test scripts/admin-qa/unit/seobulk.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { showsRetry, rowDirty, rowValues, applyResponse, viewRedirect, viewToRemember, stateText } = await import(new URL("../../../backend/web/admin/js/seobulklogic.js", import.meta.url));

test("rowDirty compares each field with its loaded copy", () => {
  assert.equal(rowDirty({ title: "a", noindex: "" }, { title: "a", noindex: "" }), false);
  assert.equal(rowDirty({ title: "b", noindex: "" }, { title: "a", noindex: "" }), true);
  assert.equal(rowDirty({ noindex: "true" }, { noindex: "" }), true);
});

test("rowValues reads switches as true or empty", () => {
  assert.deepEqual(rowValues({ title: { type: "text", value: "x" }, noindex: { type: "checkbox", checked: true } }), { title: "x", noindex: "true" });
  assert.deepEqual(rowValues({ noindex: { type: "checkbox", checked: false } }), { noindex: "" });
});

test("applyResponse maps replies to row states", () => {
  const values = { title: "T", description: "", keyword: "", noindex: true };
  const ok = applyResponse(200, { status: "saved", values });
  assert.equal(ok.state, "saved");
  assert.deepEqual(ok.orig, { title: "T", description: "", keyword: "", noindex: "true" });
  const c = applyResponse(409, { status: "conflict", message: "Changed elsewhere: title.", values, conflicts: { title: "T" } });
  assert.equal(c.state, "conflict");
  assert.deepEqual(c.refresh, ["noindex"]); // the conflicting title keeps the user's value for Retry
  assert.equal(c.text, "Changed elsewhere: title.");
  assert.equal(applyResponse(422, { status: "error", message: "Too long." }).text, "Too long.");
  const bad = applyResponse(500, null);
  assert.equal(bad.state, "error");
  assert.equal(bad.text, stateText("error"));
  assert.equal(bad.orig, null);
});

test("the SEO view is remembered and restored once", () => {
  assert.equal(viewRedirect("?type=post", "seo"), "?type=post&view=seo");
  assert.equal(viewRedirect("?view=seo", "seo"), null);
  assert.equal(viewRedirect("", ""), null);
  // the Default tab names its view, so a remembered SEO view does not bounce it back
  assert.equal(viewRedirect("?type=post&view=default", "seo"), null);
  assert.equal(viewToRemember("?view=default"), "");
  assert.equal(viewToRemember("?view=seo"), "seo");
  assert.equal(viewToRemember("?type=post"), "");
});

test("a save reply never leaves an input behind a newer orig (another user's change is shown, not reverted)", () => {
  // A loaded {title: Old t, description: Old}; B set description to Theirs; A saves a new title.
  const posted = { title: "New t", description: "Old", keyword: "", noindex: "" };
  const values = { title: "New t", description: "Theirs", keyword: "", noindex: false };
  const r = applyResponse(200, { status: "saved", values }, posted, posted);
  assert.equal(r.orig.description, "Theirs");
  assert.deepEqual(r.refresh, ["description"]);
  // the same on the non-conflicting fields of a conflict reply
  const c = applyResponse(409, { status: "conflict", values: { ...values, title: "B t" } }, posted, posted);
  assert.deepEqual(c.refresh.sort(), ["description", "title"]);
  // a field edited while the save was in flight keeps the edit
  const typed = { ...posted, description: "Mine" };
  assert.deepEqual(applyResponse(200, { status: "saved", values }, posted, typed).refresh, []);
  // nothing to refresh when the server stored what was shown
  assert.deepEqual(applyResponse(200, { status: "saved", values: { ...values, description: "Old" } }, posted, posted).refresh, []);
});

test("a failed save and a conflict offer Retry", () => {
  for (const s of ["error", "conflict"]) assert.equal(showsRetry(s), true, s);
  for (const s of ["idle", "unsaved", "saving", "saved"]) assert.equal(showsRetry(s), false, s);
});
