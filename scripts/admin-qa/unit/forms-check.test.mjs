// The Form check list. Run: node --test scripts/admin-qa/unit/forms-check.test.mjs
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
const { formCheck } = await import(new URL("forms/check.js", root));
const { GROUPS } = await import(new URL("forms/shortcuts.js", root));

const ok = (doc) => Object.fromEntries(formCheck(doc).map((c) => [c.id, c.ok]));
const f = (id, type = "text", name = id) => ({ id, type, name, label: id });

test("an empty form fails all but the names check; every item has a label and a hint", () => {
  const items = formCheck({ fields: [], settings: {} });
  assert.equal(items.length, 6);
  for (const c of items) assert.ok(c.label && c.hint, c.id);
  assert.deepEqual(ok({ fields: [], settings: {} }), { email: false, spam: false, notify: false, names: true, replyto: true, consent: false });
});

test("each item flips with the doc", () => {
  assert.equal(ok({ fields: [f("a", "email")], settings: {} }).email, true);
  assert.equal(ok({ fields: [], settings: { enable_captcha: true } }).spam, true);
  assert.equal(ok({ fields: [], settings: { notify_email: "  " } }).notify, false);
  assert.equal(ok({ fields: [], settings: { notify_email: "me@example.com" } }).notify, true);
  assert.equal(ok({ fields: [f("a", "email", "mail")], settings: { notify_reply_to: "mail" } }).replyto, true);
  assert.equal(ok({ fields: [f("a", "email", "other")], settings: { notify_reply_to: "mail" } }).replyto, false);
  assert.equal(ok({ fields: [f("a", "text", "mail")], settings: { notify_reply_to: "mail" } }).replyto, false);
  assert.equal(ok({ fields: [f("a", "text", "x"), f("b", "text", "x")], settings: {} }).names, false);
  assert.equal(ok({ fields: [f("a", "heading", ""), f("b", "divider", "")], settings: {} }).names, true);
  assert.equal(ok({ fields: [f("a")], settings: {} }).consent, false);
  assert.equal(ok({ fields: [f("a")], settings: { store_off: true } }).consent, true);
  assert.equal(ok({ fields: [f("a"), f("c", "consent")], settings: {} }).consent, true);
});

test("the shortcut sheet data has the builder's shape", () => {
  for (const [name, rows] of GROUPS) {
    assert.equal(typeof name, "string");
    for (const [keys, what] of rows) assert.ok(keys && what);
  }
  assert.equal(GROUPS.flatMap(([, r]) => r).length, 9);
});
