// The box model: padding nested in margin, quick 0 / auto buttons, link toggles with aria-pressed.
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
  const btn = (label) => find((n) => n.tag === "button" && n.attrs["aria-label"] === label)[0];
  return { m, calls, find, btn };
};
const MARGIN = ["mt", "mr", "mb", "ml"];
const PADDING = ["pt", "pr", "pb", "pl"];

test("with both rings the padding ring sits inside the margin ring, the element in the middle", () => {
  const b = make([...MARGIN, ...PADDING]);
  const outer = b.find((n) => n.className?.includes("hm-bs-ring--margin"))[0];
  assert.ok(outer.classes.has("hm-bs-ring--outer"));
  const inside = [];
  outer.walk((n) => n.className?.includes("hm-bs-ring--padding") && inside.push(n));
  assert.equal(inside.length, 1, "padding is inside the margin ring");
  assert.equal(b.find((n) => n.className === "hm-bs-ring__el").length, 1, "one element box, in the padding ring");
});

test("one ring alone still wraps the element box", () => {
  const b = make(PADDING);
  assert.equal(b.find((n) => n.className?.includes("hm-bs-ring--padding")).length, 1);
  assert.equal(b.find((n) => n.className === "hm-bs-ring__el").length, 1);
});

test("quick 0 sets one side only; auto exists on margins and never on padding", () => {
  const b = make([...MARGIN, ...PADDING], { pt: "8px", pr: "8px" });
  b.btn("pt: 0").fire("click");
  assert.deepEqual(b.calls, [["pt", "0"]], "only the pressed side");
  b.btn("ml: auto").fire("click");
  assert.deepEqual(b.calls.at(-1), ["ml", "auto"]);
  assert.equal(b.btn("pt: auto"), undefined, "padding never offers auto");
  assert.equal(b.find((n) => n.tag === "button" && /: auto$/.test(n.attrs["aria-label"] ?? "")).length, 4, "four margin sides");
});

test("quick 0 on a linked ring writes all four sides in one batch", () => {
  const b = make(PADDING);
  b.btn("Same on all sides").fire("click");
  b.btn("pl: 0").fire("click");
  assert.deepEqual(b.calls.at(-1), [["pt", "0"], ["pr", "0"], ["pb", "0"], ["pl", "0"]]);
});

test("the three link toggles are icon buttons with aria-pressed and one mode at a time", () => {
  const b = make(PADDING);
  const names = ["Same on all sides", "Same top and bottom", "Same left and right"];
  const state = () => names.map((n) => b.btn(n).attrs["aria-pressed"]);
  assert.deepEqual(state(), ["false", "false", "false"]);
  b.btn(names[0]).fire("click");
  assert.deepEqual(state(), ["true", "false", "false"]);
  b.btn(names[2]).fire("click");
  assert.deepEqual(state(), ["false", "false", "true"], "choosing a pair unlinks the ring");
  b.btn(names[2]).fire("click");
  assert.deepEqual(state(), ["false", "false", "false"]);
  for (const n of names) assert.equal(b.btn(n).attrs.title, n, "icon only: the name is also the tooltip");
});

test("a pair linked from existing equal values starts pressed", () => {
  const b = make(PADDING, { pt: "4px", pb: "4px", pr: "1px" });
  assert.equal(b.btn("Same top and bottom").attrs["aria-pressed"], "true");
  assert.equal(b.btn("Same on all sides").attrs["aria-pressed"], "false");
});

test("tab order inside a ring is top, right, bottom, left", () => {
  const b = make(PADDING);
  const order = b.find((n) => n.tag === "input" && PADDING.includes(n.attrs["aria-label"])).map((n) => n.attrs["aria-label"]);
  assert.deepEqual(order, PADDING);
});

test("the quick 0 and auto buttons are at least 24 px (WCAG 2.5.8)", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builderinspector.css", import.meta.url), "utf8");
  const rule = css.match(/\.hm-bs-quick\.hm-bs-quick \{([^}]*)\}/)[1];
  assert.match(rule, /min-width: 1\.5rem/);
  assert.match(rule, /min-height: 1\.5rem/);
});

test("the number fields of the box model hide their spin arrows (a click on a narrow field stepped the value)", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builder.css", import.meta.url), "utf8");
  assert.match(css, /\.hm-bs-len--c \.hm-bs-num::-webkit-inner-spin-button/);
});

test("hovering or focusing a padding side tells the canvas (hm-pad-hover), leaving clears it, margins stay silent", () => {
  const b = make([...MARGIN, ...PADDING]);
  // Two cells per side: the margin ring's first, the padding ring's second.
  const cell = (side) => b.find((n) => n.className === `hm-bs-side hm-bs-side--${side}`);
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
