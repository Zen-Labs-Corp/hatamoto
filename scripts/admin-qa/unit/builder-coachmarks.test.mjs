// The one-time hints: per user, dismiss persists, the first insert clears them, broken storage is safe.
// Run: node --test scripts/admin-qa/unit/builder-coachmarks.test.mjs
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

class El {
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this.textContent = ""; this.removed = false; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { this.kids.push(...k); }
  replaceChildren(...k) { this.kids = k; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  click() { for (const f of this.on.click ?? []) f({}); }
  remove() { this.removed = true; }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
}
globalThis.document = { createElement: (t) => new El(t), createElementNS: (_n, t) => new El(t), querySelector: () => null };
const store = (init = {}) => ({ data: { ...init }, getItem(k) { return this.data[k] ?? null; }, setItem(k, v) { this.data[k] = v; } });
const C = await import(new URL("builder/coachmarks.js", root));

test("hints are kept per user and dismiss persists for that user only", () => {
  const s = store();
  assert.equal(C.shouldShow(s, "u1", "add"), true);
  C.dismiss(s, "u1", "add");
  assert.equal(C.shouldShow(s, "u1", "add"), false);
  assert.equal(C.shouldShow(s, "u1", "select"), true, "the other hint is untouched");
  assert.equal(C.shouldShow(s, "u2", "add"), true);
});

test("storage that throws or holds junk shows the hints and never throws", () => {
  const bad = { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("full"); } };
  assert.equal(C.shouldShow(bad, "u", "add"), true);
  assert.doesNotThrow(() => C.dismiss(bad, "u", "add"));
  assert.equal(C.shouldShow(store({ "hm-builder-hints:u": "{nope" }), "u", "add"), true);
});

test("both hints mount as non-modal notes; Got it dismisses one for good", () => {
  const s = store(), canvasEl = new El("div"), navFoot = new El("footer");
  const m = C.mountCoachmarks({ canvasEl, navFoot, user: "u", storage: s });
  assert.equal(canvasEl.kids[0].attrs.role, "note");
  assert.equal(navFoot.kids[0].attrs.role, "note");
  canvasEl.kids[0].walk().find((n) => n.tag === "button").click();
  assert.equal(canvasEl.kids[0].removed, true);
  assert.equal(C.shouldShow(s, "u", "select"), false);
  assert.equal(C.shouldShow(s, "u", "add"), true);
  m.done();
  assert.equal(navFoot.kids[0].removed, true);
  assert.equal(C.shouldShow(s, "u", "add"), false, "the first insert dismisses the rest");
});

test("an empty page gets only the add hint; a dismissed hint is not mounted again", () => {
  const s = store(), canvasEl = new El("div"), navFoot = new El("footer");
  C.mountCoachmarks({ canvasEl, navFoot, user: "u", empty: true, storage: s });
  assert.equal(canvasEl.kids.length, 0);
  assert.equal(navFoot.kids.length, 1);
  C.dismiss(s, "u", "add");
  const again = new El("footer");
  C.mountCoachmarks({ canvasEl: new El("div"), navFoot: again, user: "u", empty: true, storage: s });
  assert.equal(again.kids.length, 0);
});

test("dismissAll ends both hints for that user (an insert made before the hint chunk loaded)", () => {
  const s = store();
  C.dismissAll(s, "u1");
  assert.equal(C.shouldShow(s, "u1", "select"), false);
  assert.equal(C.shouldShow(s, "u1", "add"), false);
  assert.equal(C.shouldShow(s, "u2", "add"), true);
});
