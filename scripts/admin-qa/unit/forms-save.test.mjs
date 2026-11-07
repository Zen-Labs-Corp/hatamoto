// The designer's saver against a fake fetch. Run: node --test scripts/admin-qa/unit/forms-save.test.mjs
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
const { createFormSaver } = await import(new URL("forms/save.js", root));

const doc = { name: "Contact", description: "", status: "active", fields: [{ id: "f_1", type: "text", label: "A", name: "a" }], settings: {} };
const res = (status, body) => ({ status, json: async () => body });
const reply = (status, body) => async () => res(status, body);
function make(fetchImpl) {
  const log = { states: [], errors: [], calls: [] };
  const saver = createFormSaver({
    url: "/save", version: "v1", getDoc: () => doc, csrf: () => "tok",
    onState: (s, m) => log.states.push([s, m]),
    onErrors: (e, s) => log.errors.push([e, s]),
    fetchImpl: (u, o) => (log.calls.push({ u, o }), fetchImpl(u, o)),
  });
  return { saver, log };
}

test("200 adopts the version, sends the definition without a slug, and goes clean", async () => {
  const { saver, log } = make(reply(200, { ok: true, version: "v2" }));
  saver.markDirty();
  await saver.save();
  assert.equal(saver.version, "v2");
  assert.equal(saver.dirty, false);
  assert.equal(log.states.at(-1)[0], "saved");
  const sent = JSON.parse(log.calls[0].o.body);
  assert.deepEqual(Object.keys(sent).sort(), ["description", "fields", "name", "settings", "status", "version"]);
  assert.equal(sent.version, "v1");
  assert.equal(log.calls[0].o.method, "PUT");
  assert.equal(log.calls[0].o.headers["X-CSRF-Token"], "tok");
});

test("409 stops further saves", async () => {
  const { saver, log } = make(reply(409, { conflict: true, version: "v9", error: "x" }));
  await saver.save();
  assert.equal(saver.state, "conflict");
  await saver.save();
  assert.equal(log.calls.length, 1);
});

test("422 maps the messages by field id and leaves saving possible", async () => {
  const { saver, log } = make(reply(422, { ok: false, error: "Some fields need attention.", errors: [{ field_id: "f_1", message: "Bad" }, { field_id: "name", message: "Enter a name" }] }));
  saver.markDirty();
  await saver.save();
  assert.deepEqual(log.errors.at(-1), [{ f_1: "Bad", name: "Enter a name" }, "Some fields need attention."]);
  assert.equal(saver.state, "error");
  assert.equal(saver.dirty, true);
  await saver.save();
  assert.equal(log.calls.length, 2);
});

test("a save in flight is not duplicated; the requests waiting behind it run once", async () => {
  let release;
  const gate = new Promise((r) => (release = r));
  let n = 0;
  const { saver, log } = make(() => (++n === 1 ? gate : Promise.resolve(res(200, { ok: true, version: "v3" }))));
  const all = [saver.save(), saver.save(), saver.save()];
  assert.equal(log.calls.length, 1);
  release(res(200, { ok: true, version: "v2" }));
  await Promise.all(all);
  assert.equal(log.calls.length, 2);
  assert.equal(saver.version, "v3");
});

test("a network error keeps the changes dirty and the next save retries", async () => {
  let fail = true;
  const { saver, log } = make(async () => {
    if (fail) throw new TypeError("offline");
    return res(200, { ok: true, version: "v2" });
  });
  saver.markDirty();
  await saver.save();
  assert.equal(saver.state, "error");
  assert.equal(saver.dirty, true);
  fail = false;
  await saver.save();
  assert.equal(saver.dirty, false);
  assert.equal(log.calls.length, 2);
});

test("an edit made during a save leaves the form dirty", async () => {
  let saver;
  ({ saver } = make(async () => {
    saver.markDirty();
    return res(200, { ok: true, version: "v2" });
  }));
  saver.markDirty();
  await saver.save();
  assert.equal(saver.dirty, true);
  assert.equal(saver.state, "pending");
});
