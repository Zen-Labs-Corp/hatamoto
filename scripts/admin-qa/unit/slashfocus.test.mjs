// "/" focuses the page search unless typing in a field. Run: node --test scripts/admin-qa/unit/slashfocus.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { isSlashFocus } from "../../../backend/web/admin/js/slashfocus.js";

test("slash on the page focuses", () => assert.equal(isSlashFocus({ key: "/", target: { tagName: "BODY" } }), true));
test("slash in a field does not", () => {
	for (const tagName of ["INPUT", "TEXTAREA", "SELECT"]) assert.equal(isSlashFocus({ key: "/", target: { tagName } }), false);
	assert.equal(isSlashFocus({ key: "/", target: { tagName: "DIV", isContentEditable: true } }), false);
});
test("modifiers and other keys do not", () => {
	assert.equal(isSlashFocus({ key: "/", ctrlKey: true, target: { tagName: "BODY" } }), false);
	assert.equal(isSlashFocus({ key: "a", target: { tagName: "BODY" } }), false);
});
