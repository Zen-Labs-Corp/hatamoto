// The box model's behaviour: arrows, typed values, link, presets, scrub, Reset, refusals.
// Run: node --test scripts/admin-qa/unit/builder-boxmodel-dom.test.mjs
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


const SPACE = { 0: "0", 4: "1rem", 7: "2rem", 10: "3rem", 12: "4rem" };
const PADDING = ["pt", "pr", "pb", "pl"];
const MARGIN = ["mt", "mr", "mb", "ml"];

// own: key -> value set here; inh: key -> inherited value; ok(entry, value) the table's verdict.
const make = (keys, { own = {}, inh = {}, ok = () => true, refuse = false } = {}) => {
  const calls = [];
  const said = [];
  const entries = full.filter((e) => keys.includes(e.key));
  const stateOf = (k) => (own[k] !== undefined ? { dot: "set", source: "local", value: own[k] } : inh[k] !== undefined ? { dot: "inherited", source: "inherited", from: "Desktop", value: inh[k] } : { dot: "none", source: "unset", value: null });
  const m = boxModel({
    entries, name: (k) => k, palette: { colors: [], space: SPACE, fs: {} }, stateOf,
    commit: (k, v, o) => (calls.push({ one: [k, v], opts: o }), true),
    commitBatch: (w, o) => (calls.push({ batch: w.map((x) => [x.key, x.value]), opts: o }), true),
    valid: ok, hint: "Use a length like 12px.", announce: (t) => said.push(t),
  });
  m.paint();
  const find = (pred) => { const r = []; m.el.walk((n) => pred(n) && r.push(n)); return r; };
  const side = (ring, name) => find((n) => n.dataset?.side === name && n.attrs["aria-label"]?.startsWith(ring === "m" ? "m" : "p"))[0];
  const btn = (label) => find((n) => n.tag === "button" && n.attrs["aria-label"] === label)[0];
  return { m, calls, said, find, side, btn };
};

test("ArrowUp/ArrowDown step by 1, Shift by 10, each press is one commit with no session", () => {
  const b = make(PADDING, { own: { pt: "16px" } });
  const pt = b.side("p", "top");
  let stopped = 0;
  pt.fire("keydown", { key: "ArrowUp", preventDefault: () => stopped++ });
  pt.fire("keydown", { key: "ArrowDown", shiftKey: true });
  assert.deepEqual(b.calls.map((c) => c.one), [["pt", "17px"], ["pt", "6px"]]);
  assert.ok(b.calls.every((c) => c.opts === undefined), "no coalesce: each press is its own undo step");
  assert.equal(stopped, 1);
});

test("an arrow on an inherited side starts from the inherited number and its unit", () => {
  const b = make(PADDING, { inh: { pt: "2rem" } });
  b.side("p", "top").fire("keydown", { key: "ArrowUp" });
  assert.deepEqual(b.calls[0].one, ["pt", "2.1rem"]);
});

test("linked: a typed value goes to all four sides in one batch; unlinked: only that side", () => {
  const b = make(PADDING);
  const pr = b.side("p", "right");
  pr.value = "12";
  pr.fire("change");
  assert.deepEqual(b.calls.at(-1).one, ["pr", "12px"], "bare number gets px, one side");
  b.btn("Link all sides").fire("click");
  pr.value = "20";
  pr.fire("change");
  assert.deepEqual(b.calls.at(-1).batch, [["pt", "20px"], ["pr", "20px"], ["pb", "20px"], ["pl", "20px"]]);
});

test("\"2rem\" is accepted and shows its unit; px is not shown", () => {
  const b = make(PADDING, { own: { pt: "2rem", pb: "48px" } });
  const pt = b.side("p", "top");
  assert.equal(pt.value, "2rem");
  assert.equal(b.side("p", "bottom").value, "48");
  pt.value = "3rem";
  pt.fire("change");
  assert.deepEqual(b.calls.at(-1).one, ["pt", "3rem"]);
});

test("garbage and refused values set aria-invalid, show the hint and do not commit", () => {
  const b = make(PADDING, { ok: (_e, v) => !v.startsWith("-") });
  const pt = b.side("p", "top");
  pt.value = "wide";
  pt.fire("change");
  assert.equal(pt.attrs["aria-invalid"], "true");
  assert.equal(b.calls.length, 0);
  const err = b.find((n) => n.attrs.class === "hm-bs-err")[0];
  assert.equal(err.hidden, false);
  assert.match(err.textContent, /Use a length like 12px\./);
  pt.value = "-4";
  pt.fire("change");
  assert.equal(pt.attrs["aria-invalid"], "true", "the table refuses it");
  assert.equal(b.calls.length, 0);
  pt.value = "4";
  pt.fire("change");
  assert.equal(pt.attrs["aria-invalid"], undefined);
  assert.equal(err.hidden, true);
  assert.deepEqual(b.calls.at(-1).one, ["pt", "4px"]);
});

test("presets: linked writes the four padding sides in one batch, unlinked only top and bottom", () => {
  const b = make([...MARGIN, ...PADDING]);
  b.btn("Padding top and bottom: M").fire("click");
  assert.deepEqual(b.calls.at(-1).batch, [["pt", "space-7"], ["pb", "space-7"]], "the theme step, top and bottom");
  b.btn("Link all sides").fire("click");
  b.btn("Padding: L").fire("click");
  assert.deepEqual(b.calls.at(-1).batch, [["pt", "space-10"], ["pr", "space-10"], ["pb", "space-10"], ["pl", "space-10"]]);
  b.btn("Padding: 0").fire("click");
  assert.deepEqual(b.calls.at(-1).batch.map((w) => w[1]), ["0", "0", "0", "0"]);
  assert.equal(b.calls.filter((c) => c.batch).length, 3, "one batch per preset");
});

test("the preset that matches the sides is pressed", () => {
  const b = make(PADDING, { own: { pt: "space-7", pb: "space-7" } });
  assert.equal(b.btn("Padding top and bottom: M").attrs["aria-pressed"], "true");
  assert.equal(b.btn("Padding top and bottom: L").attrs["aria-pressed"], "false");
});

// A drag on a number: pointerdown, moves, pointerup.
const drag = (el, moves, { shiftKey = false } = {}) => {
  el.fire("pointerdown", { clientX: 100, clientY: 100, pointerId: 1 });
  for (const [x, y] of moves) el.fire("pointermove", { clientX: x, clientY: y, shiftKey });
  el.fire("pointerup");
};

test("scrub: dragging up on a top side grows it, one coalesce key for the whole drag, final value is the last", () => {
  const b = make(PADDING, { own: { pt: "10px" } });
  drag(b.side("p", "top"), [[100, 95], [100, 90], [100, 80]]);
  const writes = b.calls.filter((c) => c.one);
  assert.deepEqual(writes.at(-1).one, ["pt", "30px"]);
  assert.ok(writes.length >= 3);
  assert.equal(new Set(writes.map((c) => c.opts.coalesce)).size, 1);
  assert.match(writes[0].opts.coalesce, /^bm:/);
});

test("scrub: right grows a left/right side; Shift is x10; down never goes below 0", () => {
  const b = make(PADDING, { own: { pl: "10px", pt: "5px" } });
  drag(b.side("p", "left"), [[110, 100]], { shiftKey: true });
  assert.deepEqual(b.calls.at(-1).one, ["pl", "110px"]);
  drag(b.side("p", "top"), [[100, 200]]);
  assert.deepEqual(b.calls.at(-1).one, ["pt", "0"]);
});

test("scrub linked: one batch per frame, all with the same coalesce key", () => {
  const b = make(PADDING, { own: { pt: "10px", pr: "10px", pb: "10px", pl: "10px" } });
  assert.equal(b.btn("Link all sides").attrs["aria-pressed"], "true");
  drag(b.side("p", "top"), [[100, 90], [100, 85]]);
  const batches = b.calls.filter((c) => c.batch);
  assert.ok(batches.length >= 2);
  assert.deepEqual(batches.at(-1).batch, [["pt", "25px"], ["pr", "25px"], ["pb", "25px"], ["pl", "25px"]]);
  assert.equal(new Set(batches.map((c) => c.opts.coalesce)).size, 1);
});

test("a click (no movement) focuses and selects instead of changing the value", () => {
  const b = make(PADDING, { own: { pt: "10px" } });
  drag(b.side("p", "top"), [[101, 100]]);
  assert.equal(b.calls.length, 0);
});

test("an inherited side is muted by data-dot and names where it is inherited from; Reset batches only the sides set here", () => {
  const b = make(PADDING, { own: { pt: "8px", pb: "8px" }, inh: { pr: "96px" } });
  const pr = b.side("p", "right");
  assert.equal(pr.dataset.dot, "inherited");
  assert.equal(pr.attrs["aria-label"], "pr, 96px, Inherited from Desktop");
  b.btn("Reset padding").fire("click");
  assert.deepEqual(b.calls.at(-1).batch, [["pt", null], ["pb", null]], "one batch = one undo step, inherited sides untouched");
});

test("Escape puts the stored value back, clears the refusal and does not reach the panel; with nothing typed it passes through", () => {
  const b = make(PADDING, { own: { pt: "16px" } });
  const pt = b.side("p", "top");
  pt.value = "wide";
  pt.fire("change");
  assert.equal(pt.attrs["aria-invalid"], "true");
  let stopped = 0;
  pt.fire("keydown", { key: "Escape", stopPropagation: () => stopped++ });
  assert.equal(pt.value, "16");
  assert.equal(pt.attrs["aria-invalid"], undefined);
  assert.equal(stopped, 1);
  pt.fire("keydown", { key: "Escape", stopPropagation: () => stopped++ });
  assert.equal(stopped, 1, "nothing to cancel: Escape keeps bubbling");
  assert.equal(b.calls.length, 0);
});

test("an arrow after typing, before Enter, steps from the typed value", () => {
  const b = make(PADDING, { own: { pt: "16px" } });
  const pt = b.side("p", "top");
  pt.value = "40";
  pt.fire("keydown", { key: "ArrowUp" });
  assert.deepEqual(b.calls[0].one, ["pt", "41px"]);
});

test("an arrow step is announced with the side and the new value", () => {
  const b = make(PADDING, { own: { pt: "16px" } });
  b.side("p", "top").fire("keydown", { key: "ArrowUp" });
  assert.deepEqual(b.said, ["pt 17px"]);
});

test("the numbers keep a text keypad (rem, auto and minus must be typeable) and the error is an alert", () => {
  const b = make([...MARGIN, ...PADDING]);
  for (const n of b.find((n) => n.dataset?.side)) assert.equal(n.attrs.inputmode, undefined);
  assert.equal(b.find((n) => n.attrs.class === "hm-bs-err")[0].attrs.role, "alert");
});

test("only the refused number points at the error text", () => {
  const b = make(PADDING);
  const [pt, pr] = [b.side("p", "top"), b.side("p", "right")];
  assert.equal(pt.attrs["aria-describedby"], undefined);
  pt.value = "wide";
  pt.fire("change");
  assert.equal(pt.attrs["aria-describedby"], b.find((n) => n.attrs.class === "hm-bs-err")[0].attrs.id);
  assert.equal(pr.attrs["aria-describedby"], undefined);
  pt.value = "4";
  pt.fire("change");
  assert.equal(pt.attrs["aria-describedby"], undefined);
});

test("a preset says which sides it writes: top and bottom while unlinked, all padding when linked", () => {
  const b = make(PADDING);
  assert.ok(b.btn("Padding top and bottom: M"));
  b.btn("Link all sides").fire("click");
  assert.ok(b.btn("Padding: M"));
  assert.equal(b.btn("Padding top and bottom: M"), undefined);
});
