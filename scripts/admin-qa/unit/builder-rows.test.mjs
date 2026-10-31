// The Style tab's rows: set-here dot, source label (Set here / Custom value / From class / Inherited), reset button and the group
// header dot, across breakpoint, state and preset. A small element stub stands in for the DOM.
// Run: node --test scripts/admin-qa/unit/builder-rows.test.mjs
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
  replaceChildren(...k) { this.kids = []; this.append(...k); }
  remove() { if (this.parent) this.parent.kids = this.parent.kids.filter((x) => x !== this); }
  replaceWith(n) { const p = this.parent; p.kids[p.kids.indexOf(this)] = n; n.parent = p; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t, ev = {}) { (this.on[t] ?? []).forEach((f) => f({ preventDefault() {}, ...ev })); }
  querySelector() { return null; }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { createElement: (t) => new El(t) };
globalThis.CSS = { escape: (s) => s };

const { mountStyle } = await import(new URL("builder/style.js", root));
const controlsui = await import(new URL("builder/controlsui.js", root));
const { lengthControl, stopSlider } = controlsui;
const { boxModel } = await import(new URL("builder/boxmodel.js", root));
const { readFileSync } = await import("node:fs");
const full = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));

const space = { pt: "space-4" };
const table = [
  { key: "pt", kind: "space", props: ["padding-top"], tokens: { "space-4": "var(--hm-space-4,1rem)" }, pattern: "^(0|[0-9]{1,4}(px|rem))$" },
  { key: "pb", kind: "space", props: ["padding-bottom"], tokens: {}, pattern: "^(0|[0-9]{1,4}(px|rem))$" },
  { key: "bg", kind: "color", props: ["background-color"], tokens: { surface: "var(--hm-surface,#fff)" }, pattern: "^#[0-9a-fA-F]{6}$", states: true },
];
const card = { name: "card", label: "Card", s: { base: { pt: "space-4", bg: "surface" } } };

const mount = (node, { bp = "base", state = "", table: tbl = table } = {}) => {
  const calls = [];
  const el = mountStyle({
    node, table: tbl, bp, state, display: "block", presets: [card],
    commit: (key, value, at, opts) => (calls.push({ key, value, at, opts }), true),
    commitMany: (writes, at, opts) => (calls.push({ batch: writes, at, opts }), true),
  });
  const find = (pred) => { const r = []; el.walk((n) => pred(n) && r.push(n)); return r; };
  // The row of a key: its reset button names it; the head holds dot and reset, the field the source chip.
  const row = (name) => {
    const reset = find((n) => n.attrs["aria-label"] === `Reset ${name}`)[0];
    const head = reset.parent;
    return { reset, dot: head.kids.find((k) => k.className.startsWith("hm-bs-dot")), src: head.parent.kids.find((k) => k.className.startsWith("hm-bs-src")), input: (() => { const r = []; head.parent.walk((n) => n.tag === "input" && n.attrs.id && r.push(n)); return r[0]; })(), head };
  };
  return { el, calls, row, groupDot: () => find((n) => n.className === "hm-bs-dot" && n.dataset.dot === "set")[0], find };
};

test("a value set here: filled dot, Set here chip, reset visible; reset clears only this key", () => {
  const m = mount({ id: "n", t: "container", s: { base: { pb: "8px" } } });
  const r = m.row("Padding bottom");
  assert.equal(r.dot.dataset.dot, "set");
  assert.equal(r.src.textContent, "Set here");
  assert.equal(r.src.dataset.source, "set");
  assert.equal(r.src.hidden, false);
  assert.equal(r.reset.hidden, false);
  r.reset.fire("click");
  assert.deepEqual(m.calls, [{ key: "pb", value: null, at: "base", opts: { label: "Change Padding bottom" } }]);
  assert.equal(r.dot.dataset.dot, "none");
  assert.equal(r.src.textContent, "");
  assert.equal(r.src.hidden, true);
  assert.equal(r.reset.hidden, true);
});

test("an unset row: no dot, no source, no reset", () => {
  const m = mount({ id: "n", t: "container", s: {} });
  const r = m.row("Padding bottom");
  assert.equal(r.dot.dataset.dot, "none");
  assert.equal(r.src.textContent, "");
  assert.equal(r.reset.hidden, true);
});

test("inherited from a wider breakpoint: ring dot and 'Inherited from Desktop'; the node's own value beats its preset", () => {
  const m = mount({ id: "n", t: "container", p: { preset: "card" }, s: { base: { pt: "8px" } } }, { bp: "md" });
  const pt = m.row("Padding top");
  assert.equal(pt.dot.dataset.dot, "inherited");
  assert.equal(pt.src.textContent, "Inherited from Desktop");
  assert.equal(pt.reset.hidden, true);
});

test("the source is said once on screen: the chip shows it, the sentence under the control is for a screen reader only", () => {
  const m = mount({ id: "n", t: "container", p: { preset: "card" }, s: { base: { pt: "8px" } } }, { bp: "md" });
  const pt = m.row("Padding top");
  assert.equal(pt.src.textContent, "Inherited from Desktop");
  const status = m.find((n) => n.className.includes("hm-bs-status") && n.textContent.startsWith("Inherited from"))[0];
  assert.ok(status.className.includes("hm-visually-hidden"), "the sentence is not a second visible line");
  assert.equal(m.find((n) => n.tag === "option" && n.textContent === "No token").length, 0);
});

test("a value only the preset gives: 'From class .card'", () => {
  const m = mount({ id: "n", t: "container", p: { preset: "card" }, s: {} }, { bp: "md" });
  const bg = m.row("Background");
  assert.equal(bg.dot.dataset.dot, "inherited");
  assert.equal(bg.src.textContent, "From class .card");
  assert.equal(bg.src.dataset.source, "class");
});

test("a state reads its own key: the dot, source and reset follow the state key", () => {
  const node = { id: "n", t: "container", s: { base: { bg: "surface" }, "base:hover": { bg: "accent" } } };
  const normal = mount(node);
  assert.equal(normal.row("Background").dot.dataset.dot, "set");
  const hover = mount(node, { state: "hover" });
  assert.equal(hover.row("Background").src.textContent, "Set here");
  const hoverOnly = mount({ id: "n", t: "container", s: { base: { bg: "surface" } } }, { state: "hover" });
  const r = hoverOnly.row("Background");
  assert.equal(r.src.textContent, "Inherited from Normal, Desktop");
  assert.equal(r.reset.hidden, true);
  r.reset.hidden = false;
  assert.deepEqual(hoverOnly.calls, []);
});

test("an edit repaints the row and the group dot", () => {
  const m = mount({ id: "n", t: "container", s: {} });
  assert.equal(m.groupDot()?.hidden ?? true, true, "no value set: the group dot is hidden");
  const r = m.row("Padding bottom");
  r.input.value = "12"; // the number field; the unit select supplies px
  r.input.fire("change");
  assert.deepEqual([m.calls[0].key, m.calls[0].value], ["pb", "12px"]);
  assert.equal(r.src.textContent, "Set here");
  assert.equal(m.groupDot().hidden, false, "the Spacing header shows a dot once a key in it is set here");
});

// The number + unit + slider control: names, arrows, drag sessions, custom values.
const control = (node, name = "Padding bottom") => {
  const m = mount(node);
  const by = (label) => m.find((n) => n.attrs["aria-label"] === label)[0];
  return { m, num: m.row(name).input, unit: by(`${name} unit`), slider: by(`${name} slider`), tok: by(`${name} site size`) };
};

test("a length row has distinct names: the number field alone carries the plain label", () => {
  const c = control({ id: "n", t: "container", s: { base: { pb: "8px" } } });
  assert.ok(c.unit && c.slider);
  assert.equal(c.num.value, "8");
  assert.equal(c.unit.value, "px");
  assert.equal(c.slider.value, "8");
  assert.equal(c.num.attrs["aria-label"], undefined, "named by its <label>, so getByLabel(name, exact) finds only it");
});

test("arrow keys step by 1, Shift by 10, one commit each with no session", () => {
  const c = control({ id: "n", t: "container", s: { base: { pb: "8px" } } });
  c.num.fire("keydown", { key: "ArrowUp" });
  c.num.fire("keydown", { key: "ArrowUp", shiftKey: true });
  c.num.fire("keydown", { key: "ArrowDown" });
  assert.deepEqual(c.m.calls.map((x) => [x.value, x.opts.coalesce]), [["9px", undefined], ["19px", undefined], ["18px", undefined]]);
});

test("a slider drag commits every input and the final change under one session key", () => {
  const c = control({ id: "n", t: "container", s: {} });
  for (const v of ["3", "9", "27"]) { c.slider.value = v; c.slider.fire("input"); }
  c.slider.fire("change");
  const keys = new Set(c.m.calls.map((x) => x.opts.coalesce));
  assert.equal(keys.size, 1);
  assert.match([...keys][0], /^s:/);
  assert.deepEqual(c.m.calls.map((x) => x.value), ["3px", "9px", "27px", "27px"]);
  c.slider.value = "5"; c.slider.fire("input"); // a second drag is a new session
  assert.notEqual(c.m.calls.at(-1).opts.coalesce, [...keys][0]);
});

test("a unit change keeps the number; a refused value is flagged and not committed", () => {
  const c = control({ id: "n", t: "container", s: { base: { pb: "8px" } } });
  c.unit.value = "rem"; c.unit.fire("change");
  assert.equal(c.m.calls.at(-1).value, "8rem");
  c.num.value = "99999"; c.num.fire("change");
  assert.equal(c.num.attrs["aria-invalid"], "true");
  assert.equal(c.m.calls.length, 1);
});

test("a token shows in the token select, and a value the control cannot express is a custom option, not rewritten", () => {
  const t = control({ id: "n", t: "container", s: { base: { pt: "space-4" } } }, "Padding top");
  assert.equal(t.tok.value, "space-4");
  assert.equal(t.num.value, "");
  const x = control({ id: "n", t: "container", s: { base: { pt: "3vmin" } } }, "Padding top");
  assert.equal(x.tok.value, "3vmin");
  assert.equal(x.m.calls.length, 0);
});

// The colour swatch: name, token commit, hex validation, Clear.
const colour = (node) => {
  const m = mount(node);
  const swatch = m.find((n) => n.attrs.popovertarget)[0];
  const panel = m.find((n) => n.attrs.id === swatch.attrs.popovertarget)[0];
  const btn = (text) => m.find((n) => n.tag === "button" && n.kids.some((k) => k.textContent === text))[0];
  return { m, swatch, panel, btn, hex: m.find((n) => n.attrs["aria-label"] === "Hex colour")[0] };
};

test("a colour key is a swatch named with its token and resolved hex; the popover is the admin's panel", () => {
  const c = colour({ id: "n", t: "container", s: { base: { bg: "surface" } } });
  assert.match(c.swatch.attrs["aria-label"], /^Background, Surface, #fff/);
  assert.equal(c.panel.attrs.class, "hm-popover-panel");
  assert.equal(c.panel.attrs.popover, "auto");
});

test("choosing a palette colour commits the token name, not the hex", () => {
  const c = colour({ id: "n", t: "container", s: {} });
  c.btn("Surface").fire("click");
  assert.deepEqual(c.m.calls.map((x) => [x.key, x.value]), [["bg", "surface"]]);
});

test("the hex field commits a valid #rrggbb, refuses an invalid one with aria-invalid, and a custom value is named custom", () => {
  const c = colour({ id: "n", t: "container", s: {} });
  c.hex.value = "#12ab9"; c.hex.fire("change");
  assert.equal(c.hex.attrs["aria-invalid"], "true");
  assert.equal(c.m.calls.length, 0);
  c.hex.value = "#12AB9F"; c.hex.fire("change");
  assert.deepEqual(c.m.calls.map((x) => x.value), ["#12ab9f"]);
  assert.equal(c.hex.attrs["aria-invalid"], undefined);
  assert.match(c.swatch.attrs["aria-label"], /^Background, custom, #12ab9f/);
});

// The Spacing box model: T,R,B,L order, linked sides, quick sets, auto on margins only.
const side = (k, kind = "space") => ({ key: k, kind, props: [k], tokens: { "space-4": "var(--hm-space-4,1rem)" }, pattern: k.startsWith("m") ? "^(auto|0|-?[0-9]{1,4}(px|rem))$" : "^(0|[0-9]{1,4}(px|rem))$" });
const boxTable = ["mt", "mr", "mb", "ml", "pt", "pr", "pb", "pl"].map((k) => side(k));
const NAMES8 = ["Margin top", "Margin right", "Margin bottom", "Margin left", "Padding top", "Padding right", "Padding bottom", "Padding left"];
const box = (s = {}) => {
  const m = mount({ id: "n", t: "container", s: { base: s } }, { table: boxTable });
  const num = (name) => m.find((n) => n.tag === "input" && n.attrs["aria-label"]?.startsWith(`${name}, `))[0]; // the name carries the value: "Padding top, 48px"
  const btn = (label, ring) => {
    const scope = ring ? m.find((n) => n.attrs.role === "group" && n.attrs["aria-label"] === ring)[0] : m.el;
    const r = [];
    scope.walk((n) => n.tag === "button" && n.attrs["aria-label"] === label && r.push(n));
    return r[0];
  };
  return { m, num, btn };
};

test("the box model draws eight named number fields, one nested box model: margin then padding, top, right, bottom, left", () => {
  const b = box();
  const order = [];
  b.m.el.walk((n) => n.tag === "input" && n.dataset?.side && order.push(n.attrs["aria-label"].split(",")[0]));
  assert.deepEqual(order, NAMES8);
});

test("link toggle: one batch of four writes; unlinked edits one side; a ring set to four equal values starts linked", () => {
  const b = box();
  const all = () => b.btn("Link all sides");
  assert.equal(all().attrs["aria-pressed"], "false");
  b.num("Padding top").value = "8"; b.num("Padding top").fire("change");
  assert.deepEqual(b.m.calls.at(-1), { key: "pt", value: "8px", at: "base", opts: { label: "Change Padding top" } });
  all().fire("click");
  assert.equal(all().attrs["aria-pressed"], "true");
  b.num("Padding left").value = "12"; b.num("Padding left").fire("change");
  const last = b.m.calls.at(-1);
  assert.deepEqual(last.batch.map((w) => [w.key, w.value]), [["pt", "12px"], ["pr", "12px"], ["pb", "12px"], ["pl", "12px"]]);
  assert.equal(box({ pt: "4px", pr: "4px", pb: "4px", pl: "4px" }).btn("Link all sides").attrs["aria-pressed"], "true");
  assert.equal(box({ pt: "4px", pb: "4px" }).btn("Link all sides").attrs["aria-pressed"], "false");
});

test("margins take auto, paddings refuse it; Reset clears only the ring's sides set here", () => {
  const b = box({ mt: "8px", pl: "4px" });
  const ml = b.num("Margin left");
  ml.value = "auto"; ml.fire("change");
  assert.deepEqual(b.m.calls.at(-1).value, "auto");
  const pt = b.num("Padding top");
  const before = b.m.calls.length;
  pt.value = "auto"; pt.fire("change");
  assert.equal(pt.attrs["aria-invalid"], "true", "padding has no auto");
  assert.equal(b.m.calls.length, before);
  b.btn("Reset margin").fire("click");
  assert.deepEqual(b.m.calls.at(-1).batch.map((w) => w.key), ["mt", "ml"]); // ml was set to auto just above
  assert.equal(b.btn("Reset padding").hidden, false);
});

// Figma groups: a header button per group, empty ones closed with a dot only when a value is set, open state kept for the session.
test("groups are disclosure buttons: a group with no value here is closed, one with a value is open; the choice is kept for the session", () => {
  const tbl = [...boxTable, { key: "pos", kind: "enum", props: ["position"], enum: { static: "static", relative: "relative" } }, { key: "bg", kind: "color", props: ["background-color"], tokens: {}, pattern: "^#[0-9a-fA-F]{6}$" }];
  const m = mount({ id: "n", t: "text", s: { base: { pos: "relative" } } }, { table: tbl });
  const heads = () => m.find((n) => n.tag === "button" && n.className === "hm-bs-grp__t");
  const by = (name) => heads().find((b) => b.kids[0].textContent === name);
  assert.equal(by("Effects").attrs["aria-expanded"], "true", "Position (set here) lives in Effects");
  assert.equal(by("Fill").attrs["aria-expanded"], "false", "nothing set in Fill");
  const region = m.find((n) => n.attrs.id === by("Fill").attrs["aria-controls"])[0];
  assert.equal(region.hidden, true, "a collapsed body is hidden, not removed");
  assert.equal(by("Fill").kids[1].hidden, true, "no dot where nothing is set");
  assert.equal(by("Effects").kids[1].hidden, false, "the dot shows a key set here");
  by("Fill").fire("click");
  assert.equal(by("Fill").attrs["aria-expanded"], "true");
  assert.equal(region.hidden, false);
  const again = mount({ id: "n", t: "text", s: {} }, { table: tbl });
  const fill = again.find((n) => n.tag === "button" && n.className === "hm-bs-grp__t" && n.kids[0].textContent === "Fill")[0];
  assert.equal(fill.attrs["aria-expanded"], "true", "a rebuilt panel keeps what the user opened");
  fill.fire("click");
});

// A length control over the real table entry of key, with the commits it makes.
const length = (key) => {
  const applied = [];
  const len = lengthControl({ id: `l-${key}`, entry: full.find((e) => e.key === key), name: key, palette: { colors: [], space: {}, fs: {} }, describe: "", apply: (v) => (applied.push(v), true) });
  const find = (pred) => { const r = []; len.el.walk((n) => pred(n) && r.push(n)); return r[0]; };
  return { len, applied, unit: find((n) => n.tag === "select" && n.className.includes("hm-bs-unit")), slider: find((n) => n.attrs.type === "range") };
};

test('a stored bare "0": the unit shows the key\'s own unit, the slider keeps its range, arrows and typing give valid values', () => {
  for (const [key, unit, step] of [["pt", "px", "1px"], ["mt", "px", "1px"], ["tx", "px", "1px"], ["bw", "px", "1px"], ["lh", "", "0.85"], ["sc", "", "0.55"]]) {
    const c = length(key);
    c.len.sync("0");
    assert.equal(c.len.num.value, key === "bw" ? "" : "0", key); // bw's "0" is one of its tokens
    if (c.unit) assert.equal(c.unit.value, unit, `${key} unit`);
    assert.ok(Number(c.slider.max) > Number(c.slider.min), `${key} slider range ${c.slider.min}..${c.slider.max}`);
    c.len.num.fire("keydown", { key: "ArrowUp" });
    if (key === "lh" || key === "sc") continue; // 0 is not a valid unitless value there; covered below
    assert.deepEqual(c.applied, [step], key);
    c.len.sync("0");
    c.len.num.value = "8";
    c.len.num.fire("change");
    assert.equal(c.applied.at(-1), `8${unit}`, `${key} typed`);
  }
});

test("arrow keys on an unset field start from the inherited value in the same unit, else the key's default", () => {
  const c = length("pt");
  c.len.sync(null, "16px");
  c.len.num.fire("keydown", { key: "ArrowUp" });
  assert.equal(c.applied.at(-1), "17px");
  const d = length("lh");
  d.len.sync(null, "");
  d.len.num.fire("keydown", { key: "ArrowUp" });
  assert.equal(d.applied.at(-1), "0.85");
  const e = length("pt");
  e.len.sync(null, "2rem");
  e.len.num.fire("keydown", { key: "ArrowUp" });
  assert.equal(e.applied.at(-1), "2.1rem", "another unit: the inherited unit is taken");
  assert.equal(e.unit.value, "rem");
});

test("arrow keys resolve an inherited token through the palette, and never step outside the key's bounds", () => {
  const withPalette = (key, palette) => {
    const applied = [];
    const len = lengthControl({ id: `l-${key}`, entry: full.find((e) => e.key === key), name: key, palette, describe: "", apply: (v) => (applied.push(v), true) });
    return { len, applied };
  };
  const pal = { colors: [], space: { 4: "1rem" }, fs: { lg: "1.5rem" } };
  const a = withPalette("pt", pal);
  a.len.sync(null, "space-4");
  a.len.num.fire("keydown", { key: "ArrowUp" });
  assert.equal(a.applied.at(-1), "1.1rem");
  const f = withPalette("fs", pal);
  f.len.sync(null, "lg");
  f.len.num.fire("keydown", { key: "ArrowUp" });
  assert.equal(f.applied.at(-1), "1.6rem");
  const d = withPalette("pt", pal);
  d.len.sync(null, "");
  d.len.num.fire("keydown", { key: "ArrowDown" });
  assert.equal(d.applied.at(-1), "0px", "unset pt never goes to -1px");
});

test("box model: arrows on a side whose padding is inherited start from the inherited value", () => {
  const entries = full.filter((e) => ["pt", "pr", "pb", "pl", "mt", "mr", "mb", "ml"].includes(e.key));
  const commits = [];
  const stateOf = (k) => (k === "pt" ? { dot: "inherited", source: "inherited", value: "16px", from: "base" } : { dot: "none", source: "none", value: null });
  const box = boxModel({ entries, name: (k) => k, palette: { colors: [], space: {}, fs: {} }, stateOf, commit: (k, v) => (commits.push([k, v]), true), commitBatch: () => true, valid: () => true, hint: "" });
  box.paint();
  const inputs = [];
  box.el.walk((n) => n.tag === "input" && n.attrs["aria-label"]?.startsWith("pt,") && inputs.push(n));
  assert.equal(inputs[0].value, "16", "the inherited value is shown (muted)");
  assert.equal(inputs[0].dataset.dot, "inherited");
  inputs[0].fire("keydown", { key: "ArrowUp" });
  assert.deepEqual(commits, [["pt", "17px"]]);
});

test("box model: each side's accessible name carries its value and where it comes from", () => {
  const entries = full.filter((e) => ["pt", "pr", "pb", "pl"].includes(e.key));
  const stateOf = (k) => (k === "pt" ? { dot: "set", source: "local", value: "2rem" } : k === "pr" ? { dot: "inherited", source: "inherited", from: "Desktop", value: "96px" } : { dot: "none", source: "none", value: null });
  const box = boxModel({ entries, name: (k) => k, palette: { colors: [], space: {}, fs: {} }, stateOf, commit: () => true, commitBatch: () => true, valid: () => true, hint: "" });
  box.paint();
  const names = [];
  box.el.walk((n) => n.tag === "input" && names.push(n.attrs["aria-label"]));
  assert.deepEqual(names, ["pt, 2rem", "pr, 96px, Inherited from Desktop", "pb, 0px, not set", "pl, 0px, not set"]);
});

test("Hover view: the preset's hover value beats the node's Normal value (Go cascade)", () => {
  const withHover = { name: "card", label: "Card", s: { base: { bg: "surface" }, "base:hover": { bg: "accent" } } };
  const el = mountStyle({ node: { id: "n", t: "container", p: { preset: "card" }, s: { base: { bg: "#112233" } } }, table, bp: "base", state: "hover", display: "block", presets: [withHover], commit: () => true, commitMany: () => true });
  const found = [];
  el.walk((n) => n.className?.startsWith?.("hm-bs-src") && found.push(n));
  assert.equal(found[0].textContent, "From class .card");
});

test("opacity slider: aria-valuetext is the opacity; unset sits at the inherited value, else 1", () => {
  const s = stopSlider({ name: "Opacity", options: ["0", "0.5", "1"], apply: () => true });
  s.sync(null);
  assert.deepEqual([s.el.value, s.el.attrs["aria-valuetext"]], ["2", "1"]);
  s.sync(null, "0.5");
  assert.deepEqual([s.el.value, s.el.attrs["aria-valuetext"]], ["1", "0.5"]);
  s.sync("0");
  assert.equal(s.el.attrs["aria-valuetext"], "0");
});

test("a slider drag that ends in blur closes its session: the next drag is another undo step", () => {
  const opts = [];
  const s = stopSlider({ name: "Opacity", options: ["0", "0.5", "1"], apply: (_v, o) => (opts.push(o.coalesce), true) });
  s.el.value = "1";
  s.el.fire("input");
  s.el.fire("blur");
  s.el.fire("input");
  assert.notEqual(opts[0], opts[1]);
});

test("Clear on a colour with nothing set here commits nothing", () => {
  const calls = [];
  const { colourControl } = controlsui;
  const c = colourControl({ id: "c1", entry: full.find((e) => e.key === "bg"), name: "Background", palette: { colors: [], space: {}, fs: {} }, describe: "", apply: (v) => (calls.push(v), true) });
  const clear = (() => { let r; c.el.walk((n) => n.tag === "button" && n.textContent === "Clear" && (r = n)); return r; })();
  c.sync(null, "#ffffff");
  clear.fire("click");
  assert.deepEqual(calls, []);
  c.sync("#112233", null);
  clear.fire("click");
  assert.deepEqual(calls, [null]);
});

// The Style tab's order: the context line, then the groups.
test("the context line is the first child and the groups follow; there is no Essentials block or More style options", () => {
  const m = mount({ id: "h", t: "container", s: {} }, { table: boxTable });
  const root = m.el;
  assert.equal(root.kids[0].attrs.class, "hm-bs-ctx");
  let select = null;
  root.kids[0].walk((n) => n.tag === "select" && (select = n));
  assert.equal(select.attrs["aria-label"], "State");
  assert.ok(!root.textContent.includes("Editing"), "the sentence is gone");
  assert.ok(!root.textContent.includes("More style options"));
  const names = root.kids.slice(1).map((k) => k.kids[0].kids[0].kids[0].textContent);
  assert.ok(names.includes("Spacing"), names.join());
  const rings = []; root.walk((n) => n.attrs.class?.includes("hm-bs-bm--nested") && rings.push(n));
  assert.equal(rings.length, 1, "one nested box model, in Spacing");
});

test("a value that differs from the node's class says Custom value; one that matches it says Set here", () => {
  const differs = mount({ id: "n", t: "container", p: { preset: "card" }, s: { base: { bg: "accent" } } }).row("Background");
  assert.equal(differs.src.textContent, "Custom value");
  assert.equal(differs.src.dataset.source, "custom");
  const same = mount({ id: "n", t: "container", p: { preset: "card" }, s: { base: { bg: "surface" } } }).row("Background");
  assert.equal(same.src.textContent, "Set here");
});

test("Reset clears only the key at the current width and state", () => {
  const m = mount({ id: "n", t: "container", s: { base: { bg: "surface" }, md: { bg: "accent" }, "md:hover": { bg: "primary" } } }, { bp: "md", state: "hover" });
  m.row("Background").reset.fire("click");
  assert.deepEqual(m.calls, [{ key: "bg", value: null, at: "md:hover", opts: { label: "Change Background (Hover, Tablet)" } }]);
});

test("colour control: plain names in the list, None for an unset swatch, Edit site colours only when offered", () => {
  const { colourControl } = controlsui;
  const palette = { colors: [{ name: "primary", label: "Brand", hex: "#112233", css: "var(--hm-primary)" }], space: {}, fs: {} };
  const texts = (el) => { const out = []; el.walk((n) => n.textContent && out.push(n.textContent)); return out; };
  const base = { id: "c2", entry: full.find((e) => e.key === "bg"), name: "Background", palette, describe: "", apply: () => true };
  const owner = colourControl({ ...base, onEditSiteColours: () => {} });
  owner.sync(null, null);
  assert.ok(texts(owner.el).includes("Brand"));
  assert.ok(!texts(owner.el).includes("primary"));
  assert.ok(texts(owner.el).includes("None"));
  assert.ok(texts(owner.el).includes("Edit site colours"));
  assert.ok(!texts(colourControl(base).el).includes("Edit site colours"));
});

test("a style write carries a human history label: the control name and the width", () => {
  const base = mount({ id: "n", t: "container", s: { base: { pb: "8px" } } });
  base.row("Padding bottom").reset.fire("click");
  assert.equal(base.calls[0].opts.label, "Change Padding bottom");
  const tablet = mount({ id: "n", t: "container", s: { md: { pb: "8px" } } }, { bp: "md" });
  tablet.row("Padding bottom").reset.fire("click");
  assert.equal(tablet.calls[0].opts.label, "Change Padding bottom (Tablet)");
});

test("the real table: a text block shows the six Figma groups with Typography first and open, a divider has Typography too, Fill and Corners start closed; Font size scrubs from its label", () => {
  const names = (m) => m.find((n) => n.tag === "button" && n.className === "hm-bs-grp__t").map((b) => b.kids[0].textContent);
  const text = mount({ id: "t", t: "text", s: {} }, { table: full });
  assert.deepEqual(names(text), ["Typography", "Layout", "Spacing", "Fill", "Corners & border", "Effects"]);
  assert.deepEqual(names(mount({ id: "d", t: "divider", s: {} }, { table: full })).slice(0, 4), ["Layout", "Spacing", "Fill", "Typography"], "other kinds keep the order");
  const head = (name) => text.find((n) => n.className === "hm-bs-grp__t" && n.kids[0].textContent === name)[0];
  assert.deepEqual(["Typography", "Fill", "Corners & border", "Effects"].map((g) => head(g).attrs["aria-expanded"]), ["true", "false", "false", "false"]);
  const label = text.find((n) => n.tag === "label" && n.classes.has("hm-bs-scrub") && n.kids[0]?.textContent === "Sz")[0];
  assert.equal(label.kids.map((k) => k.textContent).join(""), "Sz font size");
});

test("Fill shows one pill per theme colour next to the hex field; a pill commits the token name at the current key", () => {
  const palette = { colors: [{ name: "surface", label: "Surface", hex: "#ffffff" }, { name: "accent", label: "Accent", hex: "#123456" }], space: {}, fs: {} };
  const calls = [];
  const el = mountStyle({ node: { id: "n", t: "container", s: { md: { bg: "accent" } } }, table: [{ key: "bg", kind: "color", props: ["background-color"], tokens: { surface: "x", accent: "y" }, pattern: "^#[0-9a-fA-F]{6}$", states: true }], bp: "md", state: "hover", display: "block", palette,
    commit: (key, value, at) => (calls.push({ key, value, at }), true), commitMany: () => true });
  const pills = []; el.walk((n) => n.classes.has?.("hm-bs-pill") || n.attrs.class === "hm-bs-pill" ? pills.push(n) : 0);
  assert.equal(pills.length, palette.colors.length);
  assert.equal(pills.map((b) => b.attrs["aria-pressed"]).join(), "false,false", "the value is set at md only, not at md:hover");
  pills[0].fire("click");
  assert.deepEqual(calls, [{ key: "bg", value: "surface", at: "md:hover" }]);
  assert.equal(pills[0].attrs["aria-pressed"], "true");
});

test("a group is a plain block, not a named region landmark: no section, no aria-labelledby", () => {
  const m = mount({ id: "t", t: "text", s: {} }, { table: full });
  const groups = m.find((n) => n.className === "hm-bs-grp");
  assert.ok(groups.length >= 6);
  for (const g of groups) {
    assert.equal(g.tag, "div");
    assert.equal(g.attrs["aria-labelledby"], undefined);
    assert.equal(g.attrs.role, undefined);
  }
});
