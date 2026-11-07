// The forms designer's ops, types and history. Run: node --test scripts/admin-qa/unit/forms-ops.test.mjs
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
const O = await import(new URL("../../../backend/web/admin/js/forms/ops.js", import.meta.url));
const T = await import(new URL("../../../backend/web/admin/js/forms/types.js", import.meta.url));

const NAME_RE = /^[a-z][a-z0-9_]{0,63}$/;
const T0 = 1_000_000;
const f = (id, extra = {}) => ({ id, type: "text", label: id, name: id, required: false, position: 0, ...extra });
const base = (fields = []) => ({ name: "Contact", description: "", status: "active", fields, settings: { enable_captcha: false } });
const ids = (d) => d.fields.map((x) => x.id);

test("add, move and remove round-trip through undo and redo", () => {
  const before = base([f("a"), f("b")]);
  const frozen = structuredClone(before);
  let s = O.createStore(before);
  s = O.commit(s, [{ op: "addField", index: 1, field: f("c") }], { now: T0 });
  assert.deepEqual(ids(s.doc), ["a", "c", "b"]);
  s = O.commit(s, [{ op: "moveField", id: "a", to: 2 }], { now: T0 + 5000 });
  assert.deepEqual(ids(s.doc), ["c", "b", "a"]);
  s = O.commit(s, [{ op: "removeField", id: "b" }], { now: T0 + 10000 });
  assert.deepEqual(ids(s.doc), ["c", "a"]);
  s = O.jump(s, -3);
  assert.deepEqual(s.doc, frozen);
  s = O.jump(s, 3);
  assert.deepEqual(ids(s.doc), ["c", "a"]);
  assert.deepEqual(before, frozen, "the input document is never mutated");
});

test("removing a field that logic points at clears the logic, and undo restores both", () => {
  const logic = { field_id: "a", op: "filled" };
  const d = base([f("a"), f("b", { logic }), f("c", { logic: { field_id: "b", op: "empty" } })]);
  let s = O.commit(O.createStore(d), [{ op: "removeField", id: "a" }], { now: T0 });
  assert.deepEqual(ids(s.doc), ["b", "c"]);
  assert.equal(s.doc.fields[0].logic, undefined);
  assert.deepEqual(s.doc.fields[1].logic, { field_id: "b", op: "empty" });
  s = O.undo(s);
  assert.deepEqual(s.doc, d);
  s = O.redo(s);
  assert.equal(s.doc.fields[0].logic, undefined);
});

test("typing in one field coalesces; another key does not", () => {
  let s = O.createStore(base([f("a")]));
  s = O.commit(s, [{ op: "setField", id: "a", key: "label", value: "N" }], { coalesce: true, now: T0 });
  s = O.commit(s, [{ op: "setField", id: "a", key: "label", value: "Na" }], { coalesce: true, now: T0 + 300 });
  s = O.commit(s, [{ op: "setField", id: "a", key: "label", value: "Nam" }], { coalesce: true, now: T0 + 600 });
  assert.equal(s.past.length, 1);
  s = O.commit(s, [{ op: "setField", id: "a", key: "placeholder", value: "x" }], { coalesce: true, now: T0 + 700 });
  assert.equal(s.past.length, 2);
  s = O.undo(O.undo(s));
  assert.equal(s.doc.fields[0].label, "a");
  assert.equal("placeholder" in s.doc.fields[0], false);
});

test("a null value deletes the key and undo brings it back; settings and meta likewise", () => {
  let s = O.createStore(base([f("a", { help_text: "hi" })]));
  s = O.commit(s, [{ op: "setField", id: "a", key: "help_text", value: null }], { now: T0 });
  assert.equal("help_text" in s.doc.fields[0], false);
  s = O.commit(s, [{ op: "setSettings", key: "notify_email", value: "me@example.com" }, { op: "setMeta", key: "name", value: "Sales" }], { now: T0 + 5000 });
  assert.equal(s.doc.settings.notify_email, "me@example.com");
  assert.equal(s.doc.name, "Sales");
  s = O.undo(s);
  assert.equal("notify_email" in s.doc.settings, false);
  assert.equal(s.doc.name, "Contact");
  s = O.undo(s);
  assert.equal(s.doc.fields[0].help_text, "hi");
  assert.throws(() => O.applyOp(s.doc, { op: "setMeta", key: "fields", value: [] }));
  assert.throws(() => O.applyOp(s.doc, { op: "setField", id: "a", key: "id", value: "z" }));
});

test("replaceField swaps a field and undo swaps it back", () => {
  let s = O.createStore(base([f("a")]));
  s = O.commit(s, [{ op: "replaceField", id: "a", field: f("a", { type: "email" }) }], { now: T0 });
  assert.equal(s.doc.fields[0].type, "email");
  assert.equal(O.undo(s).doc.fields[0].type, "text");
});

test("a form holds at most 60 fields", () => {
  let d = base();
  for (let i = 0; i < O.MAX_FIELDS; i++) d = O.applyOp(d, { op: "addField", index: i, field: O.newField("text", d.fields) }).doc;
  assert.equal(d.fields.length, 60);
  assert.throws(() => O.applyOp(d, { op: "addField", index: 0, field: O.newField("text", []) }), (e) => e.message.includes("at most 60"));
  assert.throws(() => O.applyOp(base([f("a")]), { op: "addField", index: 0, field: f("a") }), (e) => e.message.includes("taken"));
});

test("types: 16 server types, icons, families", () => {
  assert.equal(Object.keys(T.TYPES).length, 16);
  assert.equal("file" in T.TYPES, true);
  for (const t of Object.values(T.TYPES)) assert.ok(T.GROUP_ORDER.includes(t.group));
  assert.deepEqual(T.compatible("select"), ["select", "radio", "checkbox"]);
  assert.deepEqual(T.compatible("text"), ["text", "email", "phone", "textarea", "url", "number", "date"]);
  assert.deepEqual(T.compatible("divider"), ["divider"]);
});

test("new fields have fresh ids and unique names the server accepts", () => {
  let fields = [];
  for (let i = 0; i < 3; i++) fields = [...fields, O.newField("text", fields)];
  assert.deepEqual(fields.map((x) => x.name), ["short_text", "short_text_2", "short_text_3"]);
  assert.equal(new Set(fields.map((x) => x.id)).size, 3);
  for (const type of Object.keys(T.TYPES)) {
    const n = O.newField(type, fields);
    if (!T.isLayout(type)) assert.match(n.name, NAME_RE, type);
    assert.equal(n.type, type);
    if (["select", "radio"].includes(type)) assert.ok(n.options.length >= 1);
  }
  assert.equal(O.nameFrom("Où êtes-vous ?"), "ou_etes_vous");
  assert.equal(O.nameFrom("3 things"), "field_3_things");
  assert.equal(O.nameFrom("???"), "field");
});

test("duplicating keeps names unique and inserts after the original", () => {
  let s = O.createStore(base([f("a", { name: "email" }), f("b")]));
  for (let i = 0; i < 2; i++) s = O.commit(s, O.duplicateOps(s.doc, "a"), { now: T0 + i * 5000 });
  assert.equal(s.doc.fields.length, 4);
  assert.equal(s.doc.fields[1].name, "email_3");
  assert.equal(s.doc.fields[2].name, "email_2");
  assert.equal(new Set(s.doc.fields.map((x) => x.name)).size, 4);
  assert.equal(new Set(ids(s.doc)).size, 4);
  assert.equal(s.doc.fields[0].id, "a");
  assert.equal(s.doc.fields[3].id, "b");
  assert.throws(() => O.duplicateOps(s.doc, "nope"));
});
