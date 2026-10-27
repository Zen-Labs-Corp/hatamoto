// Shell glue logic: confirm from a form button, carried email, remembered sidebar, dismissed banners.
// Run: node --test scripts/admin-qa/unit/shelllogic.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { carryHref, confirmPost, isDismissed, markDismissed, photoFailed, photoFallback, phraseMatches, rememberSidebar, rememberedSidebar, safeStorage } from "../../../backend/web/admin/js/shelllogic.js";

// A form whose controls are named like the bulk toolbar: a field called "action" shadows form.action.
const attrs = (o) => ({ getAttribute: (k) => o[k] ?? null });
class FakeFormData {
  constructor(form, submitter) {
    this.entries = [...form.fields];
    if (submitter.name) this.entries.push([submitter.name, submitter.value]);
  }
  [Symbol.iterator]() { return this.entries[Symbol.iterator](); }
}
const form = (fields, action = "/admin/people/merge") => ({ ...attrs({ action }), action: { name: "action" }, fields });
const button = (o) => ({ ...attrs(o), name: o.name, value: o.value });

test("confirmPost drops _csrf and keeps the other fields", () => {
  const got = confirmPost(form([["_csrf", "t"], ["ids", "3"], ["keep", "3"]]), button({}), FakeFormData);
  assert.equal(got.action, "/admin/people/merge");
  assert.deepEqual(got.fields, [["ids", "3"], ["keep", "3"]]);
});

test("confirmPost posts to the submitter's formaction and adds its name and value", () => {
  const got = confirmPost(form([["_csrf", "t"], ["ids", "1"]], "/admin/x"), button({ formaction: "/admin/x/bulk", name: "action", value: "delete" }), FakeFormData);
  assert.equal(got.action, "/admin/x/bulk");
  assert.deepEqual(got.fields, [["ids", "1"], ["action", "delete"]]);
});

test("confirmPost reads the action attribute, not a form.action shadowed by a control", () => {
  assert.equal(confirmPost(form([]), button({}), FakeFormData).action, "/admin/people/merge");
});

test("confirmPost hands the submitter to FormData", () => {
  let seen;
  class Spy { constructor(f, s) { seen = s; } [Symbol.iterator]() { return [][Symbol.iterator](); } }
  const b = button({});
  confirmPost(form([]), b, Spy);
  assert.equal(seen, b);
});

test("carryHref sets email and leaves an empty value alone", () => {
  assert.equal(carryHref("/admin/settings/users/new?x=1", "a@b.co", "https://h.test/admin/"), "https://h.test/admin/settings/users/new?x=1&email=a%40b.co");
  assert.equal(carryHref("/admin/settings/users/new", "", "https://h.test/"), "/admin/settings/users/new");
});

const store = (init = {}) => ({ data: { ...init }, getItem(k) { return this.data[k] ?? null; }, setItem(k, v) { this.data[k] = v; } });
const blocked = { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("blocked"); } };

test("sidebar: collapsed is restored on desktop only", () => {
  const s = store();
  assert.equal(rememberedSidebar(s, true), false);
  rememberSidebar(s, true);
  assert.equal(s.data["hm-sidebar"], "collapsed");
  assert.equal(rememberedSidebar(s, true), true);
  assert.equal(rememberedSidebar(s, false), false, "a phone has no toggle, so nothing is restored");
  rememberSidebar(s, false);
  assert.equal(s.data["hm-sidebar"], "open");
  assert.equal(rememberedSidebar(s, true), false);
});

test("sidebar: blocked storage never throws", () => {
  assert.equal(rememberedSidebar(blocked, true), false);
  assert.doesNotThrow(() => rememberSidebar(blocked, true));
});

test("notices: a dismissed banner stays dismissed per key", () => {
  const s = store();
  assert.equal(isDismissed(s, "site-address"), false);
  markDismissed(s, "site-address");
  assert.equal(isDismissed(s, "site-address"), true);
  assert.equal(isDismissed(s, "setup-pending"), false);
});

test("notices: blocked storage never throws and shows the banner", () => {
  assert.doesNotThrow(() => markDismissed(blocked, "k"));
  assert.equal(isDismissed(blocked, "k"), false);
});

test("photoFallback replaces the identity image by the initials avatar", () => {
  let replaced;
  const img = { dataset: { initials: "ED" }, replaceWith: (n) => { replaced = n; } };
  const doc = { createElement: () => ({ dataset: {} }) };
  photoFallback(img, doc);
  assert.equal(replaced.className, "hm-avatar");
  assert.equal(replaced.dataset.initials, "ED");
  assert.equal(replaced.ariaHidden, "true");
});

test("photoFallback hides the form preview so the avatar beside it shows", () => {
  const img = { dataset: {}, hidden: false };
  photoFallback(img, {});
  assert.equal(img.hidden, true);
});

test("photoFailed: loaded with no pixels", () => {
  assert.equal(photoFailed({ complete: true, naturalWidth: 0 }), true);
  assert.equal(photoFailed({ complete: true, naturalWidth: 1 }), false);
  assert.equal(photoFailed({ complete: false, naturalWidth: 0 }), false);
});

// Blocking site data makes reading window.localStorage / sessionStorage itself throw.
const blockedWindow = () => {
  const win = {};
  for (const name of ["localStorage", "sessionStorage"]) {
    Object.defineProperty(win, name, { get() { throw new Error("SecurityError"); } });
  }
  return win;
};

test("safeStorage returns null when reading the storage property throws", () => {
  const win = blockedWindow();
  assert.equal(safeStorage("local", win), null);
  assert.equal(safeStorage("session", win), null);
  const ok = store();
  assert.equal(safeStorage("local", { localStorage: ok }), ok);
});

test("blocked site data never throws through the helpers", () => {
  const win = blockedWindow();
  assert.equal(rememberedSidebar(safeStorage("local", win), true), false);
  assert.doesNotThrow(() => rememberSidebar(safeStorage("local", win), true));
  assert.equal(isDismissed(safeStorage("session", win), "k"), false);
  assert.doesNotThrow(() => markDismissed(safeStorage("session", win), "k"));
});

test("confirmPost posts to the page URL when the form has no action attribute", () => {
  globalThis.location = { href: "https://h.test/admin/x?y=1" };
  try {
    assert.equal(confirmPost(form([], null), button({}), FakeFormData).action, "https://h.test/admin/x?y=1");
  } finally { delete globalThis.location; }
});

test("an exact match passes", () => {
  assert.equal(phraseMatches("/admin", "/admin"), true);
});
test("a different case fails", () => {
  assert.equal(phraseMatches("/ADMIN", "/admin"), false);
});
test("surrounding space fails, nothing is trimmed", () => {
  assert.equal(phraseMatches("/admin ", "/admin"), false);
  assert.equal(phraseMatches(" /admin", "/admin"), false);
});
test("an empty phrase never matches, even an empty entry", () => {
  assert.equal(phraseMatches("", ""), false);
  assert.equal(phraseMatches("x", ""), false);
});
test("an empty entry fails", () => {
  assert.equal(phraseMatches("", "/admin"), false);
});
