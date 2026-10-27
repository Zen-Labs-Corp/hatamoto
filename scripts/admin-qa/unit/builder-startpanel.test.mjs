// The empty-page start panel: shows only on an empty page, tiles insert their pattern, Start blank calls back, destroy removes it.
// Run: node --test scripts/admin-qa/unit/builder-startpanel.test.mjs
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
const S = await import(new URL("builder/startpanel.js", root));

test("shouldShowStart is true only while the page has no elements", () => {
  assert.equal(S.shouldShowStart({ nodes: [] }), true);
  assert.equal(S.shouldShowStart({ nodes: [{ id: "a", t: "text" }] }), false);
});

test("pattern tiles call onPattern with their item; Start blank calls onBlank; destroy removes the panel", () => {
  const canvasEl = new El("div");
  const hero = { key: "component:hero", kind: "component", type: "hero", name: "Hero", group: "Patterns" };
  const picked = [];
  let blank = 0;
  const ui = S.mountStart(canvasEl, { patterns: [hero], onPattern: (i) => picked.push(i), onBlank: () => blank++ });
  const box = canvasEl.kids[0];
  const buttons = box.walk().filter((n) => n.tag === "button");
  assert.deepEqual(buttons.map((b) => b.textContent || b.walk().map((n) => n.textContent).join("")), ["Hero", "Start blank"]);
  assert.ok(box.walk().some((n) => n.tag === "svg"), "a thumbnail is drawn");
  buttons[0].click();
  buttons[1].click();
  assert.deepEqual(picked, [hero]);
  assert.equal(blank, 1);
  ui.destroy();
  assert.equal(box.removed, true);
});

test("at most six tiles; no patterns still offers Start blank", () => {
  const many = Array.from({ length: 9 }, (_, i) => ({ key: `component:s${i}`, kind: "component", type: `s${i}`, name: `S${i}`, group: "Patterns" }));
  const a = new El("div");
  S.mountStart(a, { patterns: many, onPattern() {}, onBlank() {} });
  assert.equal(a.kids[0].walk().filter((n) => n.tag === "li").length, 6);
  const b = new El("div");
  S.mountStart(b, { patterns: [], onPattern() {}, onBlank() {} });
  assert.equal(b.kids[0].walk().filter((n) => n.tag === "button").length, 1);
});

test("the first tiles are the usual starting blocks, and Start blank takes the panel away before calling onBlank", () => {
  const mk = (type, name) => ({ key: `component:${type}`, kind: "component", type, name, group: "Patterns" });
  const patterns = [mk("card_columns", "Card columns"), mk("comparison", "Comparison"), mk("contact", "Contact form"), mk("faq", "FAQ"), mk("features", "Features"), mk("cta", "Call to action"), mk("hero", "Hero")];
  const canvasEl = new El("div");
  let removedAtBlank = null;
  S.mountStart(canvasEl, { patterns, onPattern() {}, onBlank: () => (removedAtBlank = canvasEl.kids[0].removed) });
  const box = canvasEl.kids[0];
  const names = box.walk().filter((n) => n.tag === "li").map((li) => li.walk().filter((n) => n.textContent).map((n) => n.textContent).join(""));
  assert.deepEqual(names.slice(0, 4), ["Hero", "Features", "Call to action", "FAQ"]);
  box.walk().filter((n) => n.tag === "button").at(-1).click();
  assert.equal(removedAtBlank, true);
});
