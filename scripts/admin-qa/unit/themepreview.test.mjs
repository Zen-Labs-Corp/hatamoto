// Run: node --test scripts/admin-qa/unit/themepreview.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { previewValue } from "../../../backend/web/admin/js/themepreview.js";

test("radius accepts sizes only", () => {
	assert.equal(previewValue("--hm-preview-radius-sm", "4px"), "4px");
	assert.equal(previewValue("--hm-preview-radius-md", "0.5rem"), "0.5rem");
	assert.equal(previewValue("--hm-preview-radius-lg", "4px; x:y"), "");
	assert.equal(previewValue("--hm-preview-radius-lg", "url(x)"), "");
});
test("font accepts a plain stack only", () => {
	assert.equal(previewValue("--hm-preview-font-body", "Inter, sans-serif"), "Inter, sans-serif");
	assert.equal(previewValue("--hm-preview-font-body", "a;b"), "");
	assert.equal(previewValue("--hm-preview-font-body", "x{}"), "");
});
test("unknown property is refused", () => assert.equal(previewValue("color", "red"), ""));
