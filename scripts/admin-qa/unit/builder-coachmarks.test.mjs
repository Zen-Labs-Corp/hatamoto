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

const bar = (c) => c.kids.find((n) => n.attrs.role === "note");
const dismissBtn = (c) => bar(c).walk().find((n) => n.tag === "button");

test("the bar is kept per user and dismiss persists for that user only", () => {
  const s = store();
  assert.equal(C.shouldShow(s, "u1"), true);
  C.dismiss(s, "u1");
  assert.equal(C.shouldShow(s, "u1"), false);
  assert.equal(C.shouldShow(s, "u2"), true);
});

test("legacy flags: both hints dismissed hides the bar; one alone still shows it", () => {
  assert.equal(C.shouldShow(store({ "hm-builder-hints:u": '{"select":1,"add":1}' }), "u"), false);
  assert.equal(C.shouldShow(store({ "hm-builder-hints:u": '{"select":1}' }), "u"), true);
  assert.equal(C.shouldShow(store({ "hm-builder-hints:u": '{"add":1}' }), "u"), true);
});

test("storage that throws or holds junk shows the bar and never throws", () => {
  const bad = { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("full"); } };
  assert.equal(C.shouldShow(bad, "u"), true);
  assert.doesNotThrow(() => C.dismiss(bad, "u"));
  assert.doesNotThrow(() => C.mountCoachmarks({ canvasEl: new El("div"), user: "u", storage: bad }).done());
  assert.equal(C.shouldShow(store({ "hm-builder-hints:u": "{nope" }), "u"), true);
});

test("one non-modal note with exactly one button; Dismiss hints removes it for good", () => {
  const s = store(), canvasEl = new El("div");
  C.mountCoachmarks({ canvasEl, user: "u", storage: s });
  assert.equal(canvasEl.kids.length, 1);
  assert.equal(bar(canvasEl).attrs.class, "hm-bs-hint");
  assert.equal(bar(canvasEl).walk().filter((n) => n.tag === "button").length, 1);
  assert.equal(dismissBtn(canvasEl).attrs["aria-label"], "Dismiss hints");
  dismissBtn(canvasEl).click();
  assert.equal(bar(canvasEl).removed, true);
  assert.equal(C.shouldShow(s, "u"), false);
  const again = new El("div");
  C.mountCoachmarks({ canvasEl: again, user: "u", storage: s });
  assert.equal(again.kids.length, 0, "survives a remount");
});

test("an empty page mounts nothing", () => {
  const canvasEl = new El("div");
  const m = C.mountCoachmarks({ canvasEl, user: "u", empty: true, storage: store() });
  assert.equal(canvasEl.kids.length, 0);
  assert.doesNotThrow(() => m.done());
});

test("done() (the first insert) dismisses the bar for good; a stray navFoot is ignored", () => {
  const s = store(), canvasEl = new El("div");
  const m = C.mountCoachmarks({ canvasEl, navFoot: new El("footer"), user: "u", storage: s });
  m.done();
  assert.equal(bar(canvasEl).removed, true);
  assert.equal(C.shouldShow(s, "u"), false);
});

test("dismissAll ends the bar for that user (an insert made before the hint chunk loaded)", () => {
  const s = store();
  C.dismissAll(s, "u1");
  assert.equal(C.shouldShow(s, "u1"), false);
  assert.equal(C.shouldShow(s, "u2"), true);
  assert.equal(JSON.parse(s.data["hm-builder-hints:u1"]).select, 1);
});
