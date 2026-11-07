// The Submissions tab's pure logic. Run: node --test scripts/admin-qa/unit/forms-submissions.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const L = await import(new URL("../../../backend/web/admin/js/forms/submissionslogic.js", import.meta.url));

test("nextIndex moves one row and stops at both ends", () => {
  assert.equal(L.nextIndex(1, 5, "j"), 2);
  assert.equal(L.nextIndex(4, 5, "j"), 4);
  assert.equal(L.nextIndex(2, 5, "k"), 1);
  assert.equal(L.nextIndex(0, 5, "k"), 0);
  assert.equal(L.nextIndex(-1, 5, "j"), 0);
  assert.equal(L.nextIndex(-1, 5, "k"), 0);
  assert.equal(L.nextIndex(0, 0, "j"), -1);
  assert.equal(L.nextIndex(2, 5, "x"), 2);
});

test("shouldHandleKey ignores typing and shortcuts", () => {
  const key = (target, extra = {}) => ({ target, ...extra });
  assert.equal(L.shouldHandleKey(key({ tagName: "BODY" })), true);
  assert.equal(L.shouldHandleKey(key({ tagName: "A" })), true);
  for (const tagName of ["INPUT", "TEXTAREA", "SELECT"]) assert.equal(L.shouldHandleKey(key({ tagName })), false);
  assert.equal(L.shouldHandleKey(key({ tagName: "DIV", isContentEditable: true })), false);
  for (const m of ["ctrlKey", "metaKey", "altKey", "defaultPrevented"]) assert.equal(L.shouldHandleKey(key({ tagName: "BODY" }, { [m]: true })), false);
});

test("drawerURL keeps q, status and page and sets s", () => {
  assert.equal(L.drawerURL("/admin/forms/contact/submissions?q=ada&status=new&page=2", 7), "/admin/forms/contact/submissions?q=ada&status=new&page=2&s=7");
  assert.equal(L.drawerURL("/admin/forms/contact/submissions?s=3&page=2", "9"), "/admin/forms/contact/submissions?s=9&page=2");
  assert.equal(L.drawerURL("https://example.com/a/b", 1), "/a/b?s=1");
});

test("listURL drops s only", () => {
  assert.equal(L.listURL("/a/submissions?q=x&s=4&page=2"), "/a/submissions?q=x&page=2");
  assert.equal(L.listURL("/a/submissions?s=4"), "/a/submissions");
});
