// The builder SEO drawer: the lazy panel, the values the save carries, Escape and focus return.
// Run: node --test scripts/admin-qa/unit/builder-seodrawer.test.mjs
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
const { mountSEODrawer, readSEO } = await import(new URL("../../../backend/web/admin/js/builder/seodrawer.js", import.meta.url));
const { createSaver } = await import(new URL("../../../backend/web/admin/js/builder/sync.js", import.meta.url));

function rig() {
  const more = { getClientRects: () => [1], focus: () => log.push("focus more") };
  const log = [];
  const on = {};
  const docOn = [];
  const doc = { addEventListener: (t, f) => docOn.push([t, f]), querySelector: (sel) => (sel === "[popovertarget=bs-more]" ? more : (doc.open ?? null)), activeElement: null };
  const body = { innerHTML: "" };
  const close = { addEventListener: (t, f) => (close.fn = f), focus: () => log.push("focus close") };
  const fields = { seo_title: { value: "T" }, seo_description: { value: "D" }, seo_canonical: { value: "" }, seo_og_image: { value: "" }, seo_noindex: { checked: true } };
  const root = {
    id: "drawer",
    hidden: true,
    addEventListener: (t, f) => (on[t] = f),
    querySelector: (sel) => {
      if (sel === "[data-hm-seodrawer-body]") return body;
      if (sel === "[data-hm-seodrawer-close]") return close;
      if (sel === "[data-hm-seo]") return { dataset: {}, querySelector: () => null };
      const m = /name="(\w+)"/.exec(sel);
      return m ? fields[m[1]] : null; // no tab and no open popover in this stub
    },
  };
  const btn = { getClientRects: () => [1], attrs: {}, setAttribute: (k, v) => (btn.attrs[k] = v), focus: () => log.push("focus btn"), addEventListener: (t, f) => (btn.fn = f) };
  const edits = [];
  const fetched = [];
  const d = mountSEODrawer({
    doc,
    btn,
    root,
    urls: { seo: "/seo" },
    getDoc: () => ({ v: 2 }),
    onEdit: (v) => edits.push(v),
    fetch: async (u, o) => (fetched.push([u, o]), { ok: true, text: async () => "<div data-hm-seo></div>" }),
  });
  const esc = (e) => on.keydown(e);
  return { more, doc, d, root, btn, body, log, edits, fetched, on, close, esc };
}

test("the first open fetches the fragment as an htmx request, later opens reuse it", async () => {
  const r = rig();
  await r.d.open();
  assert.equal(r.root.hidden, false);
  assert.equal(r.btn.attrs["aria-expanded"], "true");
  assert.equal(r.body.innerHTML, "<div data-hm-seo></div>");
  assert.deepEqual(r.fetched[0][1].headers, { "HX-Request": "true", "HX-Target": "drawer" });
  r.d.close();
  await r.d.open();
  assert.equal(r.fetched.length, 1);
});

test("Escape closes the drawer and returns focus to the SEO button; Close does the same", async () => {
  const r = rig();
  await r.d.open();
  let stopped = false;
  r.esc({ key: "Escape", preventDefault() {}, stopPropagation: () => (stopped = true) });
  assert.equal(r.root.hidden, true);
  assert.equal(r.btn.attrs["aria-expanded"], "false");
  assert.deepEqual(r.log.slice(-1), ["focus btn"]);
  assert.ok(stopped);
  await r.d.open();
  r.close.fn();
  assert.equal(r.root.hidden, true);
  assert.deepEqual(r.log.slice(-1), ["focus btn"]);
});

test("Escape is left to an open dialog or popover, and to a handler that already took it", async () => {
  const r = rig();
  await r.d.open();
  let taken = false;
  const e = { key: "Escape", preventDefault: () => (taken = true), stopPropagation: () => (taken = true) };
  r.esc(e);
  assert.ok(taken && r.root.hidden);
  await r.d.open();
  taken = false;
  r.doc.open = {};
  r.esc(e);
  assert.equal(taken, false);
  assert.equal(r.root.hidden, false);
  r.doc.open = null;
  r.esc({ key: "Escape", defaultPrevented: true, preventDefault: () => (taken = true), stopPropagation: () => (taken = true) });
  assert.equal(taken, false);
  assert.equal(r.root.hidden, false);
});

test("Escape does nothing while the drawer is shut", () => {
  const r = rig();
  let stopped = false;
  r.esc({ key: "Escape", preventDefault() {}, stopPropagation: () => (stopped = true) });
  assert.equal(stopped, false);
});

test("a field edit reports the values; the analyze request carries the draft doc", async () => {
  const r = rig();
  await r.d.open();
  r.on.input({ target: { closest: () => ({}) } });
  assert.deepEqual(r.edits, [{ title: "T", description: "D", canonical: "", og_image: "", no_index: true }]);
  const e = { detail: { parameters: {} } };
  r.on["htmx:configRequest"](e);
  assert.equal(e.detail.parameters.doc, '{"v":2}');
  assert.deepEqual(readSEO(r.root).title, "T");
});

test("the saver sends no seo before an edit and the values after it", async () => {
  const bodies = [];
  let seo = null;
  const saver = createSaver({
    urls: { save: "/save" },
    boot: { status: "published", version: 1, name: "n", doc: {}, entity: "e" },
    getDoc: () => ({ v: 2 }),
    getSEO: () => seo,
    fetch: async (u, o) => (bodies.push(JSON.parse(o.body)), { status: 200, json: async () => ({ ok: true, version: 2 }) }),
  });
  await saver.save("save");
  seo = { title: "T", description: "", canonical: "", og_image: "", no_index: false };
  await saver.save("save");
  assert.equal("seo" in bodies[0], false);
  assert.deepEqual(bodies[1].seo, seo);
});

test("every open asks for the checks of the current draft", async () => {
  const r = rig();
  const fired = [];
  const panel = {};
  globalThis.htmx = { trigger: (el, ev) => fired.push([el, ev]), process() {} };
  const q = r.root.querySelector;
  r.root.querySelector = (sel) => (sel === "#be-panel" ? panel : q(sel));
  try {
    await r.d.open();
    r.d.close();
    await r.d.open();
  } finally {
    delete globalThis.htmx;
  }
  assert.deepEqual(fired, [[panel, "hm:analyze"], [panel, "hm:analyze"]]);
});

test("closing returns focus to the opener, and to the More button when the SEO button is hidden", async () => {
  const r = rig();
  const proxy = { getClientRects: () => [], focus: () => r.log.push("focus proxy") };
  r.doc.activeElement = proxy;
  r.btn.getClientRects = () => [];
  await r.d.open();
  r.d.close();
  assert.deepEqual(r.log.slice(-1), ["focus more"]);
  proxy.getClientRects = () => [1];
  await r.d.open();
  r.d.close();
  assert.deepEqual(r.log.slice(-1), ["focus proxy"]);
});
