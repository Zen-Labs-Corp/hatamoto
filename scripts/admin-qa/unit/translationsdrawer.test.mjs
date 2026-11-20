// The Translations drawer loads its panel through htmx and keeps it after the first open.
// Node has no DOM, so the elements are small stubs. Run: node --test scripts/admin-qa/unit/translationsdrawer.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { mountTranslationsDrawer } = await import(new URL("../../../backend/web/admin/js/builder/translationsdrawer.js", import.meta.url));

function rig(ajax) {
  const calls = [];
  const errors = [];
  const listeners = {};
  const body = {
    id: "hm-bs-translations-body",
    addEventListener: (t, f) => (listeners[t] ??= new Set()).add(f),
    removeEventListener: (t, f) => listeners[t]?.delete(f),
  };
  const fire = (t) => [...(listeners[t] ?? [])].forEach((f) => f({ detail: { requestConfig: { elt: body } } }));
  const root = {
    hidden: true,
    querySelector: (sel) => (sel === "[data-hm-trdrawer-body]" ? body : null),
    addEventListener() {},
  };
  const btn = { attrs: {}, setAttribute(n, v) { this.attrs[n] = v; }, focus() {} };
  const htmx = { ajax: (...a) => { calls.push(a); return (ajax ?? (() => Promise.resolve()))(fire); } };
  const drawer = mountTranslationsDrawer({ doc: { querySelector: () => null }, btn, root, url: "/p/translations", onError: (e) => errors.push(e), htmx: () => htmx });
  return { calls, errors, body, root, btn, drawer };
}

test("open asks htmx for the panel into the body, without hand-made headers", async () => {
  const r = rig();
  await r.drawer.open();
  assert.equal(r.calls.length, 1);
  const [verb, url, opts] = r.calls[0];
  assert.deepEqual([verb, url], ["GET", "/p/translations"]);
  assert.deepEqual(opts, { source: r.body, target: r.body, swap: "innerHTML" });
  assert.ok(!("headers" in opts));
  assert.equal(r.root.hidden, false);
  assert.equal(r.btn.attrs["aria-expanded"], "true");
});

test("a second open keeps the panel: no new request", async () => {
  const r = rig();
  await r.drawer.open();
  r.drawer.close();
  await r.drawer.open();
  assert.equal(r.calls.length, 1);
});

test("a failed load reports once, stays closed and is asked again on the next open", async () => {
  let n = 0;
  const r = rig((fire) => {
    if (n++ === 0) fire("htmx:responseError");
    return Promise.resolve();
  });
  await r.drawer.open();
  assert.equal(r.errors.length, 1);
  assert.equal(r.root.hidden, true);
  await r.drawer.open();
  assert.equal(r.calls.length, 2);
  assert.equal(r.root.hidden, false);
});
