// The legacy copy purge, the server recovery client and recoveryAction.
// Run: node --test scripts/admin-qa/unit/recovery.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const js = new URL("../../../backend/web/admin/js/", import.meta.url);
const { purgeLegacyCopies } = await import(new URL("recovery.js", js));
const { createRecovery, recoveryAction } = await import(new URL("editor/autosave.js", js));

const keyed = (init) => {
  const o = { ...init };
  Object.defineProperty(o, "removeItem", { value: (k) => delete o[k], enumerable: false });
  return o;
};

test("purge removes every hm:blog: key and nothing else", () => {
  const s = keyed({ "hm:blog:u1:e1": "{}", "hm:blog::e2": "x", "hm-sidebar": "open", other: "1" });
  purgeLegacyCopies(s);
  assert.deepEqual(Object.keys(s), ["hm-sidebar", "other"]);
});

test("purge survives a blocked store", () => {
  const blocked = new Proxy({}, { ownKeys() { throw new Error("denied"); } });
  assert.doesNotThrow(() => purgeLegacyCopies(blocked));
  assert.doesNotThrow(() => purgeLegacyCopies(undefined));
});

function clientRig(extra = {}) {
  const calls = [];
  let n = 0;
  const timers = {
    pending: new Map(),
    set(fn, ms) { this.pending.set(++n, { fn, ms }); return n; },
    clear(i) { this.pending.delete(i); },
    async fire() { for (const [i, t] of [...this.pending]) { this.pending.delete(i); await t.fn(); } await new Promise((r) => setTimeout(r, 5)); },
  };
  const states = [];
  let fail = false;
  let status = 204;
  let online = () => {};
  const fetch = async (url, init) => {
    if (fail) throw new TypeError("offline");
    calls.push({ url, init, body: JSON.parse(init.body) });
    return { ok: status < 300, status };
  };
  const rec = createRecovery({ url: "/r", discardUrl: "/r/d", csrf: () => "tok", fetch, timers, onState: (s) => states.push(s), onOnline: (fn) => (online = fn), ...extra });
  return { rec, calls, timers, states, goOffline: () => (fail = true), goOnline: () => ((fail = false), online()), reply: (s) => (status = s) };
}

test("a write is debounced and posts JSON with the CSRF header and credentials", async () => {
  const t = clientRig();
  t.rec.write({ content: "a", version: 1 });
  t.rec.write({ content: "b", version: 1 });
  assert.equal(t.timers.pending.size, 1);
  assert.equal([...t.timers.pending.values()][0].ms, 1500);
  await t.timers.fire();
  assert.equal(t.calls.length, 1);
  assert.equal(t.calls[0].url, "/r");
  assert.equal(t.calls[0].init.headers["X-CSRF-Token"], "tok");
  assert.equal(t.calls[0].init.headers["Content-Type"], "application/json");
  assert.equal(t.calls[0].init.credentials, "same-origin");
  assert.deepEqual(t.calls[0].body, { content: "b", version: 1 });
  assert.equal(t.rec.state, "kept");
});

test("flush sends at once with keepalive, and skips a body the browser would drop", async () => {
  const t = clientRig();
  t.rec.write({ content: "a", version: 1 });
  t.rec.flush();
  await t.timers.fire();
  assert.equal(t.calls.length, 1);
  assert.equal(t.calls[0].init.keepalive, true);
  t.rec.write({ content: "x".repeat(70000), version: 1 });
  t.rec.flush();
  assert.equal(t.calls.length, 1, "too big for keepalive: left to the debounce");
  assert.equal(t.timers.pending.size, 1, "the debounce is still armed, so Stay still sends it");
  await t.timers.fire();
  assert.equal(t.calls.length, 2);
  assert.equal(t.calls[1].init.keepalive, undefined);
});

test("the keepalive limit counts bytes, not characters", async () => {
  const t = clientRig();
  t.rec.write({ content: "\u00e9".repeat(40000), version: 1 }); // 40000 characters, 80000 bytes
  t.rec.flush();
  assert.equal(t.timers.pending.size, 1, "skipped");
  await t.timers.fire();
  assert.equal(t.calls.length, 1);
  assert.notEqual(t.calls[0].init.keepalive, true);
});

test("a suspended client sends nothing until resumed, and a clear ends the hold", async () => {
  const t = clientRig();
  t.rec.write({ content: "a", version: 1 });
  t.rec.suspend();
  t.rec.write({ content: "b", version: 1 });
  t.rec.flush();
  await t.timers.fire();
  assert.equal(t.calls.length, 0, "no timer, no flush while the person's own save runs");
  t.rec.resume();
  await t.timers.fire();
  assert.deepEqual(t.calls.map((c) => c.body.content), ["b"]);
  t.rec.suspend();
  t.rec.write({ content: "c", version: 1 });
  await t.rec.clear();
  t.rec.write({ content: "d", version: 1 });
  await t.timers.fire();
  assert.equal(t.calls.at(-1).body.content, "d", "clear() lifts the hold");
});

test("a failed write is retried with a growing delay and the status recovers", async () => {
  const t = clientRig();
  t.goOffline();
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  assert.equal(t.rec.state, "failed");
  assert.deepEqual([...t.timers.pending.values()].map((x) => x.ms), [1500]);
  await t.timers.fire();
  assert.deepEqual([...t.timers.pending.values()].map((x) => x.ms), [3000]);
  t.reply(204);
  t.goOnline.call(null);
  await t.timers.fire();
  assert.equal(t.rec.state, "kept");
  assert.equal(t.rec.pending, false);
  assert.deepEqual(t.states.at(-1), "kept");
  assert.equal(t.timers.pending.size, 0);
});

test("coming back online retries at once", async () => {
  const t = clientRig();
  t.goOffline();
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  t.goOnline();
  assert.deepEqual([...t.timers.pending.values()].map((x) => x.ms), [0]);
  await t.timers.fire();
  assert.equal(t.rec.state, "kept");
});

test("a server error is retried, a refusal and an ended session are not", async () => {
  const t = clientRig();
  t.reply(503);
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  assert.equal(t.rec.state, "failed");
  assert.equal(t.timers.pending.size, 1);
  for (const [status, state] of [[401, "expired"], [403, "expired"], [413, "refused"], [422, "refused"]]) {
    const u = clientRig();
    u.reply(status);
    u.rec.write({ content: "a", version: 1 });
    await u.timers.fire();
    assert.equal(u.rec.state, state, String(status));
    assert.equal(u.timers.pending.size, 0, `${status} is not retried`);
  }
});

test("a write typed during a retry wait replaces the queued copy", async () => {
  const t = clientRig();
  t.goOffline();
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  t.rec.write({ content: "b", version: 1 });
  assert.equal(t.timers.pending.size, 1);
  t.goOnline();
  await t.timers.fire();
  assert.equal(t.calls.at(-1).body.content, "b");
});

test("clear discards once, and only when the server may hold a copy", async () => {
  const t = clientRig();
  await t.rec.clear();
  assert.equal(t.calls.length, 0);
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  await t.rec.clear();
  await t.rec.clear();
  assert.deepEqual(t.calls.map((c) => c.url), ["/r", "/r/d"]);
  const delivered = clientRig({ copy: { content: "c", version: 1 } });
  await delivered.rec.clear();
  assert.deepEqual(delivered.calls.map((c) => c.url), ["/r/d"]);
});

test("a failed write keeps the copy pending and reports failed", async () => {
  const t = clientRig();
  t.goOffline();
  t.rec.write({ content: "a", version: 1 });
  await t.timers.fire();
  assert.equal(t.rec.state, "failed");
  assert.equal(t.rec.pending, true);
  assert.deepEqual(t.states, ["failed"]);
});

test("a cleared write in flight is not reported as kept", async () => {
  const t = clientRig();
  t.rec.write({ content: "a", version: 1 });
  const p = t.timers.fire();
  await t.rec.clear();
  await p;
  assert.notEqual(t.rec.state, "kept");
});

test("recoveryAction: none, restore, stale", () => {
  const cur = { content: "<p>a</p>", version: 3, name: "T", excerpt: "" };
  assert.equal(recoveryAction(null, cur), "none");
  assert.equal(recoveryAction({ ...cur }, cur), "none");
  assert.equal(recoveryAction({ ...cur, content: "<p>b</p>" }, cur), "restore");
  assert.equal(recoveryAction({ ...cur, content: "<p>b</p>", version: 2 }, cur), "stale");
  assert.equal(recoveryAction({ ...cur, name: "U" }, cur), "restore");
  assert.equal(recoveryAction({ ...cur, content: "x", submitted: true, version: 2 }, cur), "none");
  assert.equal(recoveryAction({ ...cur, content: "<P>a</P>" }, cur, (h) => h.toLowerCase()), "none");
});
