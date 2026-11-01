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
const { mountSEODrawer, mountSEOBadge, readSEO } = await import(new URL("../../../backend/web/admin/js/builder/seodrawer.js", import.meta.url));
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
    getName: () => "Renamed",
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
  const proxy = { getClientRects: () => [], focus: () => r.log.push("focus proxy"), matches: (s) => s.includes("data-hm-bs-proxy") };
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

test("focus returns to the SEO button when the drawer was opened from elsewhere", async () => {
  for (const active of [null, { getClientRects: () => [1], focus: () => r.log.push("focus field"), matches: () => false }]) {
    var r = rig();
    r.doc.activeElement = active;
    await r.d.open();
    r.d.close();
    assert.deepEqual(r.log.slice(-1), ["focus btn"]);
  }
});

test("opened from the SEO button, focus returns to it; with the button hidden it falls back to More", async () => {
  const r = rig();
  r.doc.activeElement = r.btn;
  await r.d.open();
  r.d.close();
  assert.deepEqual(r.log.slice(-1), ["focus btn"]);
  r.doc.activeElement = null;
  r.btn.getClientRects = () => [];
  await r.d.open();
  r.d.close();
  assert.deepEqual(r.log.slice(-1), ["focus more"]);
});

test("a More-menu control (the popover trigger) counts as the opener", async () => {
  const r = rig();
  r.doc.activeElement = r.more;
  r.more.matches = (s) => s.includes("popovertarget=bs-more");
  r.btn.getClientRects = () => [];
  await r.d.open();
  r.d.close();
  assert.deepEqual(r.log.slice(-1), ["focus more"]);
});

test("the analyze request carries the unsaved name", async () => {
  const r = rig();
  const e = { detail: { parameters: {} } };
  r.on["htmx:configRequest"](e);
  assert.equal(e.detail.parameters.name, "Renamed");
});

test("a failed check puts the note into the results and a good swap clears it", () => {
  const r = rig();
  const shown = [];
  const results = { prepend: () => shown.push(note), querySelector: (s) => (s === "[data-hm-seodrawer-shown]" ? shown[0] ?? null : null) };
  const note = { dataset: {}, remove: () => shown.pop() };
  const tpl = { content: { cloneNode: () => ({ firstElementChild: note }) } };
  const q = r.root.querySelector;
  r.root.querySelector = (sel) => (sel === "#be-panel" ? results : sel === "[data-hm-seodrawer-note]" ? tpl : q(sel));
  r.on["htmx:responseError"]({ detail: { target: results } });
  r.on["htmx:responseError"]({ detail: { target: results } }); // once only
  assert.equal(shown.length, 1);
  assert.equal(note.dataset.hmSeodrawerShown, "1");
  r.on["htmx:afterSwap"]({});
  assert.equal(shown.length, 0);
  r.on["htmx:sendError"]({ detail: { target: {} } }); // another target: ignored
  assert.equal(shown.length, 0);
});

test("showFieldError opens the Advanced tab and writes the message under the field", async () => {
  const r = rig();
  await r.d.open();
  const clicks = [];
  const tab = { click: () => clicks.push("advanced") };
  const field = { append: (p) => (field.p = p) };
  const input = { id: "seo_canonical", closest: () => field, attrs: {}, getAttribute: (k) => input.attrs[k] ?? null, removeAttribute: (k) => delete input.attrs[k], setAttribute: (k, v) => (input.attrs[k] = v), focus: () => r.log.push("focus canonical") };
  const p = { textContent: "" };
  const tpl = { content: { firstElementChild: { cloneNode: () => p } } };
  const q = r.root.querySelector;
  r.root.querySelector = (sel) => (sel === '[name="seo_canonical"]' ? input : sel === "#seo-tab-advanced" ? tab : sel === "#seo_canonical-error" ? field.p ?? null : sel === "[data-hm-seodrawer-field-error]" ? tpl : q(sel));
  await r.d.showFieldError("canonical", "Use a full address.");
  assert.deepEqual(clicks, ["advanced"]);
  assert.equal(p.textContent, "Use a full address.");
  assert.equal(p.id, "seo_canonical-error");
  assert.equal(input.attrs["aria-invalid"], "true");
  assert.equal(field.p, p);
});

test("the field error keeps the help in aria-describedby and a later edit clears the error, aria-invalid and its token", async () => {
  const r = rig();
  await r.d.open();
  const field = { append: (p) => (field.p = p) };
  const input = { id: "seo_canonical", name: "seo_canonical", attrs: { "aria-describedby": "seo_canonical-help" }, getAttribute: (k) => input.attrs[k] ?? null, removeAttribute: (k) => delete input.attrs[k], setAttribute: (k, v) => (input.attrs[k] = v), closest: () => field, focus() {} };
  const p = { textContent: "", remove: () => (field.p = null) };
  const tpl = { content: { firstElementChild: { cloneNode: () => p } } };
  const q = r.root.querySelector;
  r.root.querySelector = (sel) => (sel === '[name="seo_canonical"]' ? input : sel === "#seo_canonical-error" ? field.p ?? null : sel === "[data-hm-seodrawer-field-error]" ? tpl : q(sel));
  await r.d.showFieldError("canonical", "Bad.");
  await r.d.showFieldError("canonical", "Bad again."); // the token is added once
  assert.equal(input.attrs["aria-describedby"], "seo_canonical-help seo_canonical-error");
  assert.equal(input.attrs["aria-invalid"], "true");
  r.on.input({ target: input });
  assert.equal(field.p, null);
  assert.equal("aria-invalid" in input.attrs, false);
  assert.equal(input.attrs["aria-describedby"], "seo_canonical-help");
});

test("showFieldError with focus:false marks a loaded panel without opening it or moving focus, and does nothing before it loaded", async () => {
  const r = rig();
  const tab = { click: () => r.log.push("tab") };
  const field = { append: (p) => (field.p = p) };
  const input = { id: "seo_canonical", closest: () => field, attrs: {}, getAttribute: (k) => input.attrs[k] ?? null, removeAttribute: (k) => delete input.attrs[k], setAttribute: (k, v) => (input.attrs[k] = v), focus: () => r.log.push("focus canonical") };
  const p = { textContent: "" };
  const tpl = { content: { firstElementChild: { cloneNode: () => p } } };
  const q = r.root.querySelector;
  let loaded = false;
  r.root.querySelector = (sel) => (sel === '[name="seo_canonical"]' ? (loaded ? input : null) : sel === "#seo-tab-advanced" ? tab : sel === "#seo_canonical-error" ? field.p ?? null : sel === "[data-hm-seodrawer-field-error]" ? tpl : q(sel));
  await r.d.showFieldError("canonical", "Bad.", { focus: false });
  assert.equal(r.root.hidden, true);
  assert.equal(r.fetched.length, 0);
  loaded = true;
  await r.d.showFieldError("canonical", "Bad.", { focus: false });
  assert.equal(p.textContent, "Bad.");
  assert.equal(input.attrs["aria-invalid"], "true");
  assert.equal(r.root.hidden, true);
  assert.deepEqual(r.log, []);
});

test("a stopped save keeps the refusal code and says whether it was an autosave", async () => {
  const seen = [];
  const mk = (status) => createSaver({
    urls: { save: "/save" },
    boot: { status, version: 1, name: "n", doc: {}, entity: "e" },
    getDoc: () => ({ v: 2 }),
    onState: (q) => seen.push([q.state, q.code, q.auto]),
    timers: { set: (f) => setTimeout(f, 0), clear: clearTimeout },
    delay: 0,
    fetch: async () => ({ status: 422, json: async () => ({ code: "invalid_canonical", error: "Bad." }) }),
  });
  const auto = mk("draft");
  auto.changed();
  await new Promise((r) => setTimeout(r, 20));
  assert.deepEqual(seen.at(-1), ["stopped", "invalid_canonical", true]);
  await mk("published").save("save");
  assert.deepEqual(seen.at(-1), ["stopped", "invalid_canonical", false]);
});

test("a stopped save keeps the server's refusal code", async () => {
  const states = [];
  const saver = createSaver({
    urls: { save: "/save" },
    boot: { status: "published", version: 1, name: "n", doc: {}, entity: "e" },
    getDoc: () => ({ v: 2 }),
    onState: (q) => states.push([q.state, q.code]),
    fetch: async () => ({ status: 422, json: async () => ({ code: "invalid_canonical", error: "Bad." }) }),
  });
  await saver.save("save");
  assert.deepEqual(states.at(-1), ["stopped", "invalid_canonical"]);
});

function badgeRig(html, ok = true, fetchFn) {
  const calls = [];
  const attrs = {};
  const btn = { kids: [], append: (n) => btn.kids.push(n), querySelector: () => btn.kids.at(-1) ?? null, setAttribute: (k, v) => (attrs[k] = v), removeAttribute: (k) => delete attrs[k] };
  const mk = (tone) => ({ content: { firstElementChild: { cloneNode: () => ({ dataset: {}, tone, text: "", append(t) { this.text = t; }, remove: () => btn.kids.pop() }) } } });
  const root = { querySelector: (sel) => /"(pass|warn|fail)"/.exec(sel) && mk(/"(\w+)"/.exec(sel)[1]) };
  const parse = () => ({ querySelector: () => (html ? { className: html, querySelector: () => ({ textContent: " 82 " }) } : null) });
  const b = mountSEOBadge({ btn, root, urls: { analyze: "/analyze" }, getDoc: () => ({ v: 2 }), getName: () => "N", csrf: () => "tok", parse, fetch: fetchFn ?? (async (u, o) => (calls.push([u, o]), { ok, text: async () => "x" })) });
  return { b, btn, attrs, calls };
}

test("badge: no fetch until refresh", () => {
  const r = badgeRig("hm-seo__score hm-seo__score--pass");
  assert.equal(r.calls.length, 0);
});

test("badge fills score and tone from the analyze HTML", async () => {
  const r = badgeRig("hm-seo__score hm-seo__score--warn");
  await r.b.refresh();
  assert.equal(r.calls[0][0], "/analyze");
  assert.equal(r.calls[0][1].headers["X-CSRF-Token"], "tok");
  assert.equal(r.calls[0][1].headers["HX-Target"], "be-panel"); // with HX-Request this is what makes the server answer the fragment, not the page
  assert.equal(r.calls[0][1].body.get("name"), "N");
  assert.equal(r.calls[0][1].body.get("doc"), '{"v":2}');
  assert.equal(r.calls[0][1].body.has("seo_title"), false);
  assert.equal(r.btn.kids.length, 1);
  assert.equal(r.btn.kids[0].tone, "warn");
  assert.equal(r.btn.kids[0].text, "82");
  assert.equal(r.attrs["aria-label"], undefined); // the visible text names the button
});

test("badge stays plain on fetch failure", async () => {
  const bad = badgeRig("hm-seo__score--pass", false);
  await bad.b.refresh();
  assert.equal(bad.btn.kids.length, 0);
  const none = badgeRig(null);
  await none.b.refresh();
  assert.equal(none.btn.kids.length, 0);
  assert.equal("aria-label" in none.attrs, false);
});

test("badge: a refresh after a save replaces the score", async () => {
  const r = badgeRig("hm-seo__score--fail");
  await r.b.refresh();
  await r.b.refresh();
  assert.equal(r.btn.kids.length, 1);
  assert.equal(r.btn.kids[0].tone, "fail");
});

test("autosave resumes on the next edit after an invalid_canonical refusal", async () => {
  const bodies = [];
  const saver = createSaver({
    urls: { save: "/save" },
    boot: { status: "draft", version: 1, name: "n", doc: {}, entity: "e" },
    getDoc: () => ({ v: 2 }),
    timers: { set: (f) => setTimeout(f, 0), clear: clearTimeout },
    delay: 0,
    fetch: async (u, o) => (bodies.push(1), bodies.length === 1 ? { status: 422, json: async () => ({ code: "invalid_canonical", error: "Bad." }) } : { status: 200, json: async () => ({ ok: true, version: 2 }) }),
  });
  saver.changed();
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(saver.q.state, "stopped");
  saver.changed(); // the person fixed the canonical
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(bodies.length, 2);
  assert.equal(saver.q.state, "saved");
});

test("badge: a refresh that fails after a good one leaves the button plain", async () => {
  let ok = true;
  const r = badgeRig("hm-seo__score--pass", true, async () => ({ ok, text: async () => "x" }));
  await r.b.refresh();
  assert.equal(r.btn.kids.length, 1);
  ok = false;
  await r.b.refresh();
  assert.equal(r.btn.kids.length, 0);
  assert.equal("aria-label" in r.attrs, false);
});

test("badge: a score the drawer set while a refresh was in flight is not overwritten by it", async () => {
  let release;
  const r = badgeRig("hm-seo__score--fail", true, () => new Promise((res) => (release = () => res({ ok: true, text: async () => "x" }))));
  const pending = r.b.refresh();
  r.b.set({ className: "hm-seo__score--pass", querySelector: () => ({ textContent: "99" }) });
  release();
  await pending;
  assert.equal(r.btn.kids.length, 1);
  assert.equal(r.btn.kids[0].tone, "pass");
  assert.equal(r.btn.kids[0].text, "99");
});
