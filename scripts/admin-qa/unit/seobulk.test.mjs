// The inline SEO view's pure logic. Run: node --test scripts/admin-qa/unit/seobulk.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/seobulklogic") return { url: ${JSON.stringify(new URL("../../../backend/web/admin/js/seobulklogic.js", import.meta.url).href)}, shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: "data:text/javascript,export const safeStorage=()=>null;", shortCircuit: true };
      return next(s, c);
    }`),
);
const { mountSEOBulk } = await import(new URL("../../../backend/web/admin/js/seobulk.js", import.meta.url));

const { showsRetry, rowDirty, rowValues, applyResponse, viewRedirect, viewToRemember, stateText } = await import(new URL("../../../backend/web/admin/js/seobulklogic.js", import.meta.url));

test("rowDirty compares each field with its loaded copy", () => {
  assert.equal(rowDirty({ title: "a", noindex: "" }, { title: "a", noindex: "" }), false);
  assert.equal(rowDirty({ title: "b", noindex: "" }, { title: "a", noindex: "" }), true);
  assert.equal(rowDirty({ noindex: "true" }, { noindex: "" }), true);
});

test("rowValues reads switches as true or empty", () => {
  assert.deepEqual(rowValues({ title: { type: "text", value: "x" }, noindex: { type: "checkbox", checked: true } }), { title: "x", noindex: "true" });
  assert.deepEqual(rowValues({ noindex: { type: "checkbox", checked: false } }), { noindex: "" });
});

test("applyResponse maps replies to row states", () => {
  const values = { title: "T", description: "", keyword: "", noindex: true };
  const ok = applyResponse(200, { status: "saved", values });
  assert.equal(ok.state, "saved");
  assert.deepEqual(ok.orig, { title: "T", description: "", keyword: "", noindex: "true" });
  const c = applyResponse(409, { status: "conflict", message: "Changed elsewhere: title.", values, conflicts: { title: "T" } });
  assert.equal(c.state, "conflict");
  assert.deepEqual(c.refresh, ["noindex"]); // the conflicting title keeps the user's value for Retry
  assert.equal(c.text, "Changed elsewhere: title is now “T”. Retry keeps yours.");
  assert.deepEqual(c.held, { title: "T" });
  assert.equal(applyResponse(422, { status: "error", message: "Too long." }).text, "Too long.");
  const bad = applyResponse(500, null);
  assert.equal(bad.state, "error");
  assert.equal(bad.text, stateText("error"));
  assert.equal(bad.orig, null);
});

test("the SEO view is remembered and restored once", () => {
  assert.equal(viewRedirect("?type=post", "seo"), "?type=post&view=seo");
  assert.equal(viewRedirect("?view=seo", "seo"), null);
  assert.equal(viewRedirect("", ""), null);
  // the Default tab names its view, so a remembered SEO view does not bounce it back
  assert.equal(viewRedirect("?type=post&view=default", "seo"), null);
  assert.equal(viewToRemember("?view=default"), "");
  assert.equal(viewToRemember("?view=seo"), "seo");
  assert.equal(viewToRemember("?type=post"), "");
});

test("a save reply never leaves an input behind a newer orig (another user's change is shown, not reverted)", () => {
  // A loaded {title: Old t, description: Old}; B set description to Theirs; A saves a new title.
  const posted = { title: "New t", description: "Old", keyword: "", noindex: "" };
  const values = { title: "New t", description: "Theirs", keyword: "", noindex: false };
  const r = applyResponse(200, { status: "saved", values }, posted, posted);
  assert.equal(r.orig.description, "Theirs");
  assert.deepEqual(r.refresh, ["description"]);
  // the same on the non-conflicting fields of a conflict reply
  const c = applyResponse(409, { status: "conflict", values: { ...values, title: "B t" } }, posted, posted);
  assert.deepEqual(c.refresh.sort(), ["description", "title"]);
  // a field edited while the save was in flight keeps the edit
  const typed = { ...posted, description: "Mine" };
  assert.deepEqual(applyResponse(200, { status: "saved", values }, posted, typed).refresh, []);
  // nothing to refresh when the server stored what was shown
  assert.deepEqual(applyResponse(200, { status: "saved", values: { ...values, description: "Old" } }, posted, posted).refresh, []);
});

test("a failed save and a conflict offer Retry", () => {
  for (const s of ["error", "conflict"]) assert.equal(showsRetry(s), true, s);
  for (const s of ["idle", "unsaved", "saving", "saved"]) assert.equal(showsRetry(s), false, s);
});

// A row with a held conflict, driven through the real mount with a stub DOM and fetch.
function rig() {
  const handlers = {};
  const form = { id: "r1" };
  const inputs = {};
  const input = (name, type, value) => (inputs[name] = { name, type, value, checked: type === "checkbox" && value === "true", disabled: false, form });
  for (const [n, v] of [["title", "Old"], ["description", "D"], ["keyword", ""]]) input(n, "text", v), input("orig_" + n, "hidden", v);
  input("noindex", "checkbox", ""), input("orig_noindex", "hidden", "");
  const text = { textContent: "" };
  const state = { dataset: {}, querySelector: () => text };
  const label = { textContent: "Save" };
  const btn = { hidden: true, ownerDocument: { activeElement: null }, querySelector: () => label };
  Object.assign(form, {
    elements: { namedItem: (n) => inputs[n] ?? null },
    querySelector: (sel) => (sel === ".hm-savestate" ? state : sel === "[data-hm-seobulk-save]" ? btn : null),
    getAttribute: () => "/seo",
  });
  const doc = { addEventListener: (t, f) => (handlers[t] ??= []).push(f), querySelector: () => null, querySelectorAll: () => [btn] };
  const posts = [];
  const replies = [];
  globalThis.FormData = class {
    constructor(f) { this.e = Object.values(inputs).filter((i) => !i.disabled && i.name).map((i) => [i.name, i.type === "checkbox" ? (i.checked ? "true" : null) : i.value]).filter(([, v]) => v !== null); }
    [Symbol.iterator]() { return this.e[Symbol.iterator](); }
  };
  globalThis.fetch = async (_url, o) => {
    posts.push(Object.fromEntries(o.body));
    const next = replies.shift();
    const r = await next.gate;
    return { status: r?.http ?? next.http, json: async () => next.body };
  };
  const fire = (t, target) => handlers[t].forEach((f) => f({ target, preventDefault() {} }));
  const edit = (n, v) => { inputs[n].value = v; if (n === "noindex") inputs[n].checked = v === "true"; fire("input", inputs[n]); fire("change", inputs[n]); };
  return { api: mountSEOBulk(doc), form, inputs, state, text, btn, posts, replies, edit, fire, handlers };
}
const conflict = (title) => ({ http: 409, body: { status: "conflict", conflicts: { title }, values: { title, description: "D", keyword: "", noindex: false } } });
const saved = (v) => ({ http: 200, body: { status: "saved", values: { keyword: "", noindex: false, ...v } } });
const tick = () => new Promise((r) => setTimeout(r, 0));

test("after a conflict a queued edit of another field does not re-send the conflicting title", async () => {
  const t = rig();
  let open;
  t.replies.push({ ...conflict("Theirs"), gate: new Promise((r) => (open = r)) }, { ...saved({ title: "Theirs", description: "Mine d" }), gate: null });
  t.edit("title", "Mine");
  await tick();
  t.edit("description", "Mine d"); // queued while the title save is in flight
  open();
  await tick(); await tick();
  assert.equal(t.posts.length, 2);
  assert.equal("orig_title" in t.posts[1], false, "orig_title posted after a conflict");
  assert.equal(t.inputs.title.value, "Mine");
  assert.equal(t.state.dataset.state, "conflict");
  assert.match(t.text.textContent, /“Theirs”/);
});

test("after a conflict a Noindex toggle posts the row without the conflicting title", async () => {
  const t = rig();
  t.replies.push({ ...conflict("Theirs"), gate: null }, { ...saved({ title: "Theirs" }), gate: null });
  t.edit("title", "Mine");
  await tick(); await tick();
  t.edit("noindex", "true");
  await tick(); await tick();
  assert.equal(t.posts.length, 2);
  assert.equal("orig_title" in t.posts[1], false);
  assert.equal(t.posts[1].orig_noindex, "");
  assert.equal(t.state.dataset.state, "conflict", "the conflict notice stays");
  assert.equal(t.inputs.title.value, "Mine");
});

test("Retry posts the user's title against the other user's value", async () => {
  const t = rig();
  t.replies.push({ ...conflict("Theirs"), gate: null }, { ...saved({ title: "Mine" }), gate: null });
  t.edit("title", "Mine");
  await tick(); await tick();
  t.handlers.submit.forEach((f) => f({ target: t.form, preventDefault() {} }));
  await tick(); await tick();
  assert.equal(t.posts[1].title, "Mine");
  assert.equal(t.posts[1].orig_title, "Theirs");
  assert.equal(t.state.dataset.state, "saved");
});
