// The box model's structure: padding nested in margin, one text number per side in T,R,B,L order, one link toggle,
// presets, the hm-pad-hover contract. Behaviour (typing, arrows, scrub, batches): builder-boxmodel-dom.test.mjs.
// Run: node --test scripts/admin-qa/unit/builder-boxmodel.test.mjs
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
  constructor(tag) {
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, hidden: false, dataset: {}, className: "", textContent: "", value: "", checked: false, classes: new Set() });
    this.style = { setProperty: (k, v) => (this.props = { ...this.props, [k]: v }), removeProperty: (k) => delete this.props?.[k] };
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)), add: (c) => this.classes.add(c) };
    this.dataset = {};
  }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "hidden") this.hidden = true; if (k === "class") this.className = String(v); if (k.startsWith("data-")) this.dataset[k.slice(5)] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  toggleAttribute(k, on) { if (on) this.attrs[k] = ""; else delete this.attrs[k]; }
  matches() { return false; }
  append(...k) { for (const x of k) { this.kids.push(x); x.parent = this; } }
  remove() { if (this.parent) this.parent.kids = this.parent.kids.filter((x) => x !== this); }
  replaceWith(n) { const p = this.parent; p.kids[p.kids.indexOf(this)] = n; n.parent = p; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  focus() {} select() {} setPointerCapture() {}
  dispatchEvent(e) { (globalThis.__events ??= []).push({ type: e.type, side: e.detail.side, bubbles: e.bubbles }); }
  fire(t, ev = {}) { (this.on[t] ?? []).forEach((f) => f({ preventDefault() {}, ...ev })); }
  querySelector() { return null; }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { createElement: (t) => new El(t) };
globalThis.CSS = { escape: (s) => s };

const { boxModel } = await import(new URL("builder/boxmodel.js", root));
import { readFileSync } from "node:fs";
const full = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));

const make = (keys, own = {}) => {
  const calls = [];
  const entries = full.filter((e) => keys.includes(e.key));
  const stateOf = (k) => (own[k] !== undefined ? { dot: "set", source: "local", value: own[k] } : { dot: "none", source: "unset", value: null });
  const m = boxModel({ entries, name: (k) => k, palette: { colors: [], space: {}, fs: {} }, stateOf, commit: (k, v) => (calls.push([k, v]), true), commitBatch: (w) => (calls.push(w.map((x) => [x.key, x.value])), true), valid: () => true, hint: "" });
  m.paint();
  const find = (pred) => { const r = []; m.el.walk((n) => pred(n) && r.push(n)); return r; };
  const cls = (c) => find((n) => n.className?.split(" ").includes(c));
  const btn = (label) => find((n) => n.tag === "button" && n.attrs["aria-label"] === label)[0];
  return { m, calls, find, cls, btn };
};
const MARGIN = ["mt", "mr", "mb", "ml"];
const PADDING = ["pt", "pr", "pb", "pl"];

test("with both rings the padding ring sits inside the margin ring, the element in the middle", () => {
  const b = make([...MARGIN, ...PADDING]);
  assert.ok(b.m.el.classes.has("hm-bs-bm--nested") || b.m.el.className.includes("hm-bs-bm--nested"));
  const [outer] = b.cls("hm-bs-bm__m");
  const inside = [];
  outer.walk((n) => n.className === "hm-bs-bm__p" || n.className?.startsWith("hm-bs-bm__p ") ? inside.push(n) : null);
  assert.equal(inside.length, 1, "padding is inside the margin ring");
  assert.equal(b.cls("hm-bs-bm__el").length, 1, "one element box, in the padding ring");
});

test("one ring alone still wraps the element box", () => {
  const b = make(PADDING);
  assert.equal(b.cls("hm-bs-bm__p").length, 1);
  assert.equal(b.cls("hm-bs-bm__el").length, 1);
  assert.equal(b.cls("hm-bs-bm__m").length, 0);
});

test("each side is one text input; no quick buttons per side and no spin arrows to step by accident", () => {
  const b = make([...MARGIN, ...PADDING]);
  const nums = b.cls("hm-bs-bm__n");
  assert.equal(nums.length, 8);
  for (const n of nums) assert.equal(n.attrs.type, "text");
  assert.equal(b.find((n) => n.tag === "select").length, 0);
});

test("one link toggle with aria-pressed, starting pressed only when every ring with values has four equal ones", () => {
  const b = make(PADDING);
  assert.equal(b.find((n) => n.attrs["aria-label"] === "Link all sides").length, 1, "a single toggle");
  assert.equal(b.btn("Link all sides").attrs["aria-pressed"], "false");
  b.btn("Link all sides").fire("click");
  assert.equal(b.btn("Link all sides").attrs["aria-pressed"], "true");
  b.btn("Link all sides").fire("click");
  assert.equal(b.btn("Link all sides").attrs["aria-pressed"], "false");
  const same = make(PADDING, { pt: "4px", pr: "4px", pb: "4px", pl: "4px" });
  assert.equal(same.btn("Link all sides").attrs["aria-pressed"], "true");
  const mixed = make(PADDING, { pt: "4px", pb: "4px", pr: "1px" });
  assert.equal(mixed.btn("Link all sides").attrs["aria-pressed"], "false");
});

test("tab order is top, right, bottom, left, margin ring before padding ring", () => {
  const b = make([...MARGIN, ...PADDING]);
  const order = b.cls("hm-bs-bm__n").map((n) => n.dataset.side);
  assert.deepEqual(order.slice(0, 4), ["top", "right", "bottom", "left"]);
  const names = b.cls("hm-bs-bm__n").map((n) => n.attrs["aria-label"].split(",")[0]);
  assert.deepEqual(names, ["mt", "mr", "mb", "ml", "pt", "pr", "pb", "pl"].map((k) => k));
});

test("every number has an accessible name with its value; an inherited one says so, an unset one says not set", () => {
  const entries = full.filter((e) => PADDING.includes(e.key));
  const stateOf = (k) => ({ pt: { dot: "set", source: "local", value: "48px" }, pr: { dot: "inherited", source: "inherited", from: "Desktop", value: "96px" } })[k] ?? { dot: "none", source: "unset", value: null };
  const m = boxModel({ entries, name: (k) => `Padding ${k}`, palette: { space: {} }, stateOf, commit: () => true, commitBatch: () => true, valid: () => true, hint: "" });
  m.paint();
  const label = (side) => { let a; m.el.walk((n) => n.dataset?.side === side && (a = n.attrs["aria-label"])); return a; };
  assert.equal(label("top"), "Padding pt, 48px");
  assert.equal(label("right"), "Padding pr, 96px, inherited");
  assert.equal(label("bottom"), "Padding pb, 0px, not set");
});

test("the presets and number fields are at least 24 px tall (WCAG 2.5.8)", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builderinspector.css", import.meta.url), "utf8");
  const quick = css.match(/\.hm-bs-quick\.hm-bs-quick \{([^}]*)\}/)[1];
  assert.match(quick, /min-width: 1\.5rem/);
  assert.match(quick, /min-height: 1\.5rem/);
  assert.match(css.match(/\.hm-bs-bm__n \{([^}]*)\}/)[1], /min-block-size: 1\.5rem/);
});

test("hovering or focusing a padding side tells the canvas (hm-pad-hover), leaving clears it, margins stay silent", () => {
  const b = make([...MARGIN, ...PADDING]);
  const cell = (side) => b.cls("hm-bs-bm__n").filter((n) => n.dataset.side === side);
  globalThis.__events = [];
  const [marginTop, padTop] = cell("top");
  const padRight = cell("right")[1];
  padTop.fire("pointerenter");
  padRight.fire("focusin");
  padRight.fire("focusout");
  padTop.fire("pointerleave");
  assert.deepEqual(globalThis.__events, [
    { type: "hm-pad-hover", side: "top", bubbles: true },
    { type: "hm-pad-hover", side: "right", bubbles: true },
    { type: "hm-pad-hover", side: null, bubbles: true },
    { type: "hm-pad-hover", side: null, bubbles: true },
  ]);
  globalThis.__events = [];
  marginTop.fire("pointerenter");
  marginTop.fire("focusin");
  assert.deepEqual(globalThis.__events, [], "the margin ring dispatches nothing");
});
