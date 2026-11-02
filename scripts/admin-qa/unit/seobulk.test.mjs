// The inline SEO view's pure logic. Run: node --test scripts/admin-qa/unit/seobulk.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { rowDirty, rowValues, applyResponse, viewRedirect, viewToRemember, stateText } = await import(new URL("../../../backend/web/admin/js/seobulklogic.js", import.meta.url));

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
  assert.deepEqual(c.refresh, ["title"]);
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
  assert.equal(viewToRemember("?view=seo"), "seo");
  assert.equal(viewToRemember("?type=post"), "");
});
