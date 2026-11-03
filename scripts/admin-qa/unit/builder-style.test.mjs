// The Style tab's pure helpers: which controls a node gets from the StyleSpec table,
// inherited values across base/md/sm, and value validity against the table.
// Run: node --test scripts/admin-qa/unit/builder-style.test.mjs
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
const { groupsFor, control, check, positioned, inheritedAt, stateOptions } = await import(new URL("../../../backend/web/admin/js/builder/style.js", import.meta.url));
const { BPS, inheritedValue, ownValue, NAMES, hiddenAt } = await import(new URL("../../../backend/web/admin/js/builder/bp.js", import.meta.url));

// The shape of GET /admin/builder/stylespec.
const table = [
  { key: "pt", kind: "space", props: ["padding-top"], tokens: { "space-4": "1rem", "space-8": "2rem" }, pattern: "^-?\\d+(\\.\\d+)?(px|rem|em|%)$" },
  { key: "gap", kind: "space", props: ["gap"], tokens: { "space-4": "1rem" }, pattern: "^-?\\d+(\\.\\d+)?(px|rem|em|%)$" },
  { key: "dir", kind: "enum", props: ["flex-direction"], enum: { row: "row", column: "column" } },
  { key: "cols", kind: "count", props: ["grid-template-columns"], min: 1, max: 12, tpl: "repeat(%d,minmax(0,1fr))" },
  { key: "rows", kind: "count", props: ["grid-template-rows"], min: 1, max: 12, tpl: "repeat(%d,auto)" },
  { key: "span", kind: "count", props: ["grid-column"], min: 1, max: 12, tpl: "span %d" },
  { key: "as", kind: "enum", props: ["align-self"], enum: { start: "flex-start", center: "center", end: "flex-end", stretch: "stretch" } },
  { key: "ai", kind: "enum", props: ["align-items"], enum: { start: "flex-start", center: "center" } },
  { key: "ta", kind: "enum", props: ["text-align"], enum: { left: "left", center: "center" } },
  { key: "hide", kind: "enum", props: ["display"], enum: { 1: "none" } },
  { key: "ml", kind: "space", props: ["margin-left"], tokens: { "space-4": "1rem" }, pattern: "^(0|-?\\d+(\\.\\d+)?(px|rem|em|%)|auto)$" },
  { key: "pos", kind: "enum", props: ["position"], enum: { static: "static", sticky: "sticky", relative: "relative" } },
  { key: "top", kind: "space", props: ["top"], tokens: {}, pattern: "^(0|-?\\d+(px|rem))$" },
  { key: "z", kind: "count", props: ["z-index"], min: 0, max: 100, tpl: "%d" },
  { key: "order", kind: "count", props: ["order"], min: -1, max: 12, tpl: "%d" },
  { key: "bgimg", kind: "media", props: ["background-image"], pattern: "^/media/[a-z0-9]+\\.jpg$", settle: true },
  { key: "display", kind: "enum", props: ["display"], enum: { flex: "flex", none: "none" }, settle: true },
  { key: "bg", kind: "color", props: ["background-color"], tokens: { primary: "var(--hm-primary)" }, pattern: "^#[0-9a-fA-F]{6}$" },
];
const entry = (k) => table.find((e) => e.key === k);

test("groupsFor: flex controls for a container, grid controls for a grid, parent controls inside a container", () => {
  const flex = groupsFor(table, { id: "a", t: "container" });
  assert.deepEqual(flex.map((g) => g.name), ["Layout", "Spacing", "Position", "Typography", "Background", "Visibility"]);
  assert.deepEqual(flex[0].entries.map((e) => e.key), ["dir", "ai", "gap"]);
  const grid = groupsFor(table, { id: "a", t: "container", p: { layout: "grid" } });
  assert.deepEqual(grid[0].entries.map((e) => e.key), ["cols", "rows", "gap", "ai"]);
  const text = groupsFor(table, { id: "b", t: "text" });
  assert.deepEqual(text.map((g) => g.name), ["Spacing", "Position", "Typography", "Background", "Visibility"]);
  const inFlex = groupsFor(table, { id: "c", t: "text" }, { id: "a", t: "container" });
  assert.deepEqual(inFlex.find((g) => g.name === "In its parent").entries.map((e) => e.key), ["as", "order"]);
  const inGrid = groupsFor(table, { id: "c", t: "text" }, { id: "a", t: "container", p: { layout: "grid" } });
  assert.deepEqual(inGrid.find((g) => g.name === "In its parent").entries.map((e) => e.key), ["span", "as", "order"]);
  const inCols = groupsFor(table, { id: "c", t: "container" }, { id: "a", t: "columns" });
  assert.deepEqual(inCols.find((g) => g.name === "In its parent").entries.map((e) => e.key), ["span", "as", "order"]);
  assert.equal(groupsFor(table, { id: "c", t: "text" }, { id: "a", t: "tabs" }).some((g) => g.name === "In its parent"), false);
  assert.deepEqual(groupsFor([], { t: "container" }), []);
});

test("every label is defined for a key the groups can show", () => {
  for (const e of table) assert.ok(NAMES[e.key], e.key);
});

test("groupsFor: the position offsets and stack order show once any breakpoint positions the node", () => {
  const keys = (n) => groupsFor(table, n).find((g) => g.name === "Position").entries.map((e) => e.key);
  assert.deepEqual(keys({ t: "text" }), ["pos"]);
  assert.deepEqual(keys({ t: "text", s: { base: { pos: "static" } } }), ["pos"]);
  assert.deepEqual(keys({ t: "text", s: { sm: { pos: "sticky" } } }), ["pos", "top", "z"]);
  assert.equal(positioned({ t: "text", s: { md: { pos: "relative" } } }), true);
});

test("labels of the v2 keys come with the lazy chunk, and zero-based counts get a number field only", () => {
  for (const k of ["ml", "pos", "top", "z", "order", "bgimg", "display"]) assert.ok(NAMES[k], k);
  assert.deepEqual(control(entry("z")), { kind: "buttons", options: [], min: 0, max: 100, number: true });
  assert.deepEqual([control(entry("order")).min, control(entry("order")).options], [-1, []]);
  assert.equal(check(entry("z"), "0"), true);
  assert.equal(check(entry("order"), "-1"), true);
  assert.equal(check(entry("order"), "-0"), false);
  assert.equal(check(entry("ml"), "auto"), true);
  assert.equal(check(entry("bgimg"), "/media/a.jpg"), true);
  assert.equal(check(entry("bgimg"), "/media/../a.jpg"), false);
});

test("control: small enums and counts are button groups, others select or text", () => {
  assert.deepEqual(control(entry("dir")), { kind: "buttons", options: [["row", "row"], ["column", "column"]] });
  assert.equal(control(entry("as")).kind, "buttons");
  assert.deepEqual(control(entry("ta")).kind, "select");
  const cols = control(entry("cols"));
  assert.deepEqual(cols.options, [["1", "1"], ["2", "2"], ["3", "3"], ["4", "4"]]);
  assert.deepEqual([cols.kind, cols.number, cols.min, cols.max], ["buttons", true, 1, 12]);
  assert.equal(control(entry("span")).number, true);
  assert.deepEqual(control(entry("pt")), { kind: "length" }); // number + unit + slider; the token select reads entry.tokens
  assert.deepEqual(control(entry("bgimg")), { kind: "text", suggestions: [] }); // free text keeps the suggestions
});

test("hiddenAt: hide cascades, display re-shows, hide beats display at one width", () => {
  const n = { id: "x", t: "text", s: { md: { hide: "1" } } };
  assert.deepEqual([hiddenAt(n, "base"), hiddenAt(n, "md"), hiddenAt(n, "sm")], [false, true, true]);
  assert.equal(hiddenAt({ id: "y", t: "text" }, "sm"), false);
  // mobile-only: hidden on desktop and tablet, shown on phone
  const mobileOnly = { t: "text", s: { base: { hide: "1" }, sm: { display: "flex" } } };
  assert.deepEqual(BPS.map((b) => hiddenAt(mobileOnly, b)), [true, true, false]);
  assert.equal(hiddenAt({ t: "text", s: { sm: { hide: "1", display: "flex" } } }, "sm"), true, "hide beats display at one width");
  assert.equal(hiddenAt({ t: "text", s: { base: { display: "none" } } }, "md"), true);
});


test("inheritedValue: nearest wider breakpoint wins, base has none", () => {
  const n = { id: "x", t: "container", s: { base: { pt: "space-8", gap: "space-4" }, md: { pt: "space-4" } } };
  assert.equal(inheritedValue(n, "base", "pt"), null);
  assert.deepEqual(inheritedValue(n, "md", "pt"), { value: "space-8", bp: "base" });
  assert.deepEqual(inheritedValue(n, "sm", "pt"), { value: "space-4", bp: "md" });
  assert.deepEqual(inheritedValue(n, "sm", "gap"), { value: "space-4", bp: "base" });
  assert.equal(inheritedValue(n, "sm", "bg"), null);
  assert.equal(ownValue(n, "md", "pt"), "space-4");
  assert.equal(ownValue(n, "sm", "pt"), null);
  assert.equal(inheritedValue({ id: "y", t: "text" }, "sm", "pt"), null);
});

test("check follows the table: tokens, patterns, enums, cols range", () => {
  assert.equal(check(entry("pt"), "space-4"), true);
  assert.equal(check(entry("pt"), "12px"), true);
  assert.equal(check(entry("pt"), "huge"), false);
  assert.equal(check(entry("dir"), "row"), true);
  assert.equal(check(entry("dir"), "diagonal"), false);
  assert.equal(check(entry("cols"), "3"), true);
  assert.equal(check(entry("cols"), "13"), false);
  assert.equal(check(entry("bg"), "#a1b2c3"), true);
  assert.equal(check(entry("bg"), "red"), false);
  assert.equal(check(entry("bg"), "primary"), true);
});

test("a numeric enum lists its options in numeric order", () => {
  const op = { key: "op", kind: "enum", enum: { 0: "0", 0.05: "0.05", 0.5: "0.5", 1: "1" } };
  assert.deepEqual(control(op).options.map(([v]) => v), ["0", "0.05", "0.5", "1"]);
});

test("groupsFor in a state lists only states keys, and Transition only in Normal", () => {
  const st = [
    ...table.filter((e) => e.key !== "bg"),
    { key: "bg", kind: "color", props: ["background-color"], tokens: {}, states: true },
    { key: "ty", kind: "lit", props: ["transform"], settle: true, states: true },
    { key: "trans", kind: "enum", props: ["transition-property"], enum: { all: "all" }, settle: true },
  ];
  const names = (g) => g.map((x) => x.name);
  assert.deepEqual(names(groupsFor(st, { id: "a", t: "container" }, null, "hover")), ["Background", "Effects"]);
  assert.deepEqual(groupsFor(st, { id: "a", t: "container" }, null, "hover").flatMap((g) => g.entries.map((e) => e.key)), ["bg", "ty"]);
  assert.ok(names(groupsFor(st, { id: "a", t: "container" })).includes("Transition"));
  assert.deepEqual(groupsFor(table, { id: "a", t: "container" }), groupsFor(table, { id: "a", t: "container" }, null, ""));
});

test("inheritedAt: wider breakpoints of the same state, then Normal at this width and wider", () => {
  const n = { s: { base: { bg: "n-base" }, md: { bg: "n-md" }, "base:hover": { bg: "h-base" } } };
  assert.deepEqual(inheritedAt(n, "md:hover", "bg"), { value: "h-base", bp: "base:hover" });
  assert.deepEqual(inheritedAt(n, "sm:hover", "bg"), { value: "h-base", bp: "base:hover" });
  assert.deepEqual(inheritedAt(n, "base:hover", "bg"), { value: "n-base", bp: "base" });
  assert.deepEqual(inheritedAt({ s: { md: { bg: "x" } } }, "md:focus", "bg"), { value: "x", bp: "md" });
  assert.equal(inheritedAt({ s: { md: { bg: "x" } } }, "base:focus", "bg"), null);
  assert.deepEqual(inheritedAt(n, "sm", "bg"), inheritedValue(n, "sm", "bg"));
});

test("stateOptions: Parent hover only with a parent that is not a ref or component", () => {
  const ids = (p) => stateOptions(p).map(([id]) => id);
  assert.deepEqual(ids({ t: "container" }), ["", "hover", "focus", "active", "parent-hover"]);
  for (const p of [null, { t: "ref" }, { t: "component" }]) assert.deepEqual(ids(p), ["", "hover", "focus", "active"]);
});

// ---- mountStyle: the Layout group's primary rows, More and the width buttons ----------------

class El {
  constructor(tag) {
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, hidden: false, dataset: {}, className: "", textContent: "", value: "", checked: false, placeholder: "", classes: new Set() });
    this.style = { setProperty() {}, removeProperty() {} };
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
const { mountStyle } = await import(new URL("../../../backend/web/admin/js/builder/style.js", import.meta.url));
const { readFileSync } = await import("node:fs");
const full = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));

const FS = { sm: "0.875rem", md: "1rem", xl: "1.5rem", "2xl": "2rem" };
const mount = (node, { bp = "base", state = "", parent = null, fs = FS } = {}) => {
  const calls = [];
  const el = mountStyle({
    node, parent, table: full, bp, state, display: "block", presets: [],
    palette: { colors: [], space: { 4: "1rem" }, fs },
    commit: (key, value, at, opts) => (calls.push({ key, value, at, opts }), true),
    commitMany: (writes, at, opts) => (calls.push({ batch: writes, at, opts }), true),
  });
  const find = (pred, from = el) => { const r = []; from.walk((n) => pred(n) && r.push(n)); return r; };
  const group = (name) => find((n) => n.className === "hm-bs-grp").find((g) => g.kids[0].kids[0].kids[0].kids[0].textContent === name);
  return { el, calls, find, group };
};
const section = (s = {}) => ({ id: "n", t: "container", s: { base: s } });

test("group headings are h4 with the button inside, which keeps aria-expanded", () => {
  const m = mount(section());
  const head = m.group("Layout").kids[0].kids[0];
  assert.equal(head.tag, "h4");
  assert.equal(head.kids[0].tag, "button");
  assert.ok(["true", "false"].includes(head.kids[0].attrs["aria-expanded"]));
});

test("a section's Layout shows Width, Max width, Align and Gap/Height without a select or a slider; the rest is behind More", () => {
  const m = mount(section());
  const layout = m.group("Layout");
  const more = m.find((n) => n.className === "hm-bs-grp__more", layout)[0];
  const inMore = new Set(m.find(() => true, more));
  const primary = m.find((n) => (n.tag === "select" || n.attrs.type === "range") && !inMore.has(n), layout);
  assert.deepEqual(primary, [], "no select, no slider in the primary rows");
  const labels = m.find((n) => n.className?.includes("hm-bs-fld__lb") && !inMore.has(n), layout).map((n) => n.kids[0]?.textContent ?? n.textContent);
  assert.deepEqual(labels, ["W", "Gap", "H"]);
  const rows = m.find((n) => n.className === "hm-field hm-bs-field hm-bs-lrow", layout);
  assert.equal(rows.length, 1, "the Max width row");
  assert.equal(rows[0].kids[0].textContent, "Max width", "its label sits in the row's label column");
  assert.ok(m.find((n) => n.className === "hm-bs-fld", rows[0]).length, "beside the inline scrub field");
  const widths = m.find((n) => n.dataset?.v && ["full", "wide", "text"].includes(n.dataset.v), layout).map((b) => b.textContent);
  assert.deepEqual(widths, ["Full", "Wide", "Text"]);
  const toggle = m.find((n) => n.className === "hm-bs-morebtn", layout)[0];
  assert.equal(toggle.textContent, "More layout options");
  assert.equal(toggle.attrs["aria-expanded"], "false");
  assert.equal(more.hidden, true);
  assert.ok(m.find((n) => n.attrs?.["aria-label"] === "Reset Row gap" || n.textContent === "Row gap", more).length, "legacy rows stay in More");
});

test("More opens by itself when a key behind it has a value here, not for a primary key", () => {
  const withMore = mount(section({ rgap: "8px" }));
  const t = withMore.find((n) => n.className === "hm-bs-morebtn", withMore.group("Layout"))[0];
  assert.equal(t.attrs["aria-expanded"], "true");
  assert.equal(withMore.find((n) => n.className === "hm-bs-grp__more", withMore.group("Layout"))[0].hidden, false);
  const primaryOnly = mount(section({ maxw: "full" }));
  assert.equal(primaryOnly.find((n) => n.className === "hm-bs-morebtn", primaryOnly.group("Layout"))[0].attrs["aria-expanded"], "false");
});

test("the More toggle shows and hides its rows", () => {
  const m = mount(section());
  const layout = m.group("Layout");
  const t = m.find((n) => n.className === "hm-bs-morebtn", layout)[0];
  const box = m.find((n) => n.className === "hm-bs-grp__more", layout)[0];
  t.fire("click");
  assert.equal(t.attrs["aria-expanded"], "true");
  assert.equal(box.hidden, false);
  t.fire("click");
  assert.equal(box.hidden, true);
});

test("Width buttons: pressed state follows maxw (full, container, 45rem), another value presses none", () => {
  const pressed = (maxw) => {
    const m = mount(section(maxw === null ? {} : { maxw }));
    return m.find((n) => ["full", "wide", "text"].includes(n.dataset?.v) && n.attrs["aria-pressed"] === "true").map((b) => b.dataset.v);
  };
  assert.deepEqual(pressed("full"), ["full"]);
  assert.deepEqual(pressed("container"), ["wide"]);
  assert.deepEqual(pressed("45rem"), ["text"]);
  assert.deepEqual(pressed("720px"), []);
  assert.deepEqual(pressed(null), []);
});

test("Width buttons write maxw through the one commit path; the active one unsets; the Max width field follows", () => {
  const m = mount(section());
  const btn = (v) => m.find((n) => n.dataset?.v === v && n.tag === "button")[0];
  btn("wide").fire("click");
  assert.deepEqual(m.calls.at(-1), { key: "maxw", value: "container", at: "base", opts: { label: "Change Max width" } });
  assert.equal(btn("wide").attrs["aria-pressed"], "true");
  btn("text").fire("click");
  assert.equal(m.calls.at(-1).value, "45rem");
  assert.equal(btn("wide").attrs["aria-pressed"], "false");
  btn("text").fire("click");
  assert.equal(m.calls.at(-1).value, null, "pressing the active one unsets it");
  btn("full").fire("click");
  const field = m.find((n) => n.tag === "input" && n.attrs["aria-describedby"] && n.attrs.type === "text" && n.value === "full");
  assert.equal(field.length, 1, "the Max width field shows the token");
});

test("a block that is not a section keeps its Layout behind More", () => {
  const m = mount({ id: "i", t: "image", s: { base: {} } }, { parent: section() });
  const layout = m.group("Layout");
  assert.equal(m.find((n) => n.dataset?.v === "full", layout).length, 0, "no Width buttons");
  assert.equal(m.find((n) => n.className === "hm-bs-morebtn", layout).length, 1);
});

// ---- Corners, Effects, Typography and a text block's Spacing -------------------------------

const heading = (s = {}) => ({ id: "h", t: "heading", s: { base: s } });
const press = (m, label, group) => m.find((n) => n.tag === "button" && n.attrs["aria-label"] === label, group ? m.group(group) : m.el)[0];

test("Corners & border: radius and border width sit two-up; the hint stays", () => {
  const m = mount(section());
  const g = m.group("Corners & border");
  const r2 = m.find((n) => n.className === "hm-bs-r2", g);
  assert.equal(r2.length, 1);
  assert.deepEqual(m.find((n) => n.className?.includes("hm-bs-fld__lb"), r2[0]).map((n) => n.kids[0]?.textContent), ["R", "B"]);
  assert.ok(m.find((n) => n.textContent === "A width shows only once a border style is set.", g).length);
});

test("Effects: Shadow is None|S|M|L buttons writing none|sm|md|lg; the active one unsets; the rest is behind More", () => {
  const m = mount(section());
  const g = m.group("Effects");
  const row = m.find((n) => n.className === "hm-bs-opts", g)[0];
  assert.deepEqual(row.kids.map((b) => b.textContent), ["None", "S", "M", "L"]);
  assert.deepEqual(row.kids.map((b) => b.attrs["aria-label"]), ["None", "Small", "Medium", "Large"]);
  for (const [name, v] of [["Small", "sm"], ["Medium", "md"], ["Large", "lg"], ["None", "none"]]) {
    press(m, name, "Effects").fire("click");
    assert.deepEqual(m.calls.at(-1), { key: "shadow", value: v, at: "base", opts: { label: "Change Shadow" } });
  }
  press(m, "None", "Effects").fire("click");
  assert.equal(m.calls.at(-1).value, null, "pressing the active one unsets");
  assert.equal(m.find((n) => n.attrs?.["aria-label"] === "Opacity slider" || n.attrs?.["aria-label"] === "Reset Opacity", g).length > 0, true, "opacity stays reachable");
  assert.equal(m.find((n) => n.className === "hm-bs-morebtn", g)[0].textContent, "More effects options");
});

test("a heading: Typography is first and open; Sz and Wt, LH and LS are two-up; Align has Auto|L|C|R|J", () => {
  const m = mount(heading(), { parent: section() });
  const groups = m.find((n) => n.className === "hm-bs-grp").map((g) => g.kids[0].kids[0].kids[0].kids[0].textContent);
  assert.equal(groups[0], "Typography");
  const typo = m.group("Typography");
  assert.equal(typo.kids[0].kids[0].kids[0].attrs["aria-expanded"], "true");
  const r2 = m.find((n) => n.className === "hm-bs-r2", typo);
  assert.equal(r2.length, 2);
  assert.deepEqual(m.find((n) => n.className === "hm-bs-opts" && n.kids[0]?.textContent === "Auto", typo)[0].kids.map((b) => b.textContent), ["Auto", "L", "C", "R", "J"]);
  press(m, "Center", "Typography").fire("click");
  assert.deepEqual(m.calls.at(-1), { key: "ta", value: "center", at: "base", opts: { label: "Change Text align" } });
  press(m, "Auto", "Typography").fire("click");
  assert.equal(m.calls.at(-1).value, null, "Auto unsets");
});

test("the Text style select writes the size token and the weight in one batch with one label", () => {
  const m = mount(heading(), { parent: section() });
  const sel = m.find((n) => n.tag === "select" && n.kids.some((o) => o.textContent === "Custom"), m.group("Typography"))[0];
  assert.deepEqual(sel.kids.map((o) => o.attrs.value), ["", "2xl", "xl", "md", "sm"], "one option per site size, biggest first");
  sel.value = "2xl";
  sel.fire("change");
  assert.deepEqual(m.calls.at(-1), { batch: [{ key: "fs", value: "2xl" }, { key: "fw", value: "bold" }], at: "base", opts: { label: "Change Text style" } });
  assert.equal(sel.value, "2xl", "it shows the preset now that both keys match");
  sel.value = "sm";
  sel.fire("change");
  assert.deepEqual(m.calls.at(-1).batch, [{ key: "fs", value: "sm" }, { key: "fw", value: "normal" }]);
});

test("the Text style select reads Custom when fs and fw do not match a preset, and is absent without site sizes", () => {
  const sel = (m) => m.find((n) => n.tag === "select" && n.kids.some((o) => o.textContent === "Custom"), m.group("Typography"))[0];
  assert.equal(sel(mount(heading({ fs: "xl", fw: "bold" }))).value, "xl");
  assert.equal(sel(mount(heading({ fs: "xl", fw: "400" }))).value, "");
  assert.equal(sel(mount(heading(), { fs: {} })), undefined);
});

test("a text block's Spacing is two margin fields and a hint with the section's gap; the other sides are behind More", () => {
  const parent = section({ gap: "space-4" });
  const m = mount(heading(), { parent });
  const g = m.group("Spacing");
  assert.equal(m.find((n) => n.className?.includes("hm-bs-bm"), g).length, 0, "no box model");
  const r2 = m.find((n) => n.className === "hm-bs-r2", g)[0];
  assert.deepEqual(m.find((n) => n.className?.includes("hm-bs-fld__lb"), r2).map((n) => n.kids[0]?.textContent), ["\u2191", "\u2193"]);
  assert.equal(m.find((n) => n.className === "hm-bs-hint", g)[0].textContent, "Space between blocks comes from the section's Gap (16px).");
  const more = m.find((n) => n.className === "hm-bs-grp__more", g)[0];
  for (const side of ["Padding top", "Padding left", "Margin right"]) assert.ok(m.find((n) => n.attrs?.["aria-label"] === `Reset ${side}`, more).length, side);
  assert.equal(mount(heading(), { parent: section() }).find((n) => n.className === "hm-bs-hint").map((n) => n.textContent)[0], "Space between blocks comes from the section's Gap.");
});

test("a section keeps the box model in Spacing, no More", () => {
  const m = mount(section());
  const g = m.group("Spacing");
  assert.equal(m.find((n) => n.className === "hm-bs-bm hm-bs-bm--nested", g).length, 1);
  assert.equal(m.find((n) => n.className === "hm-bs-morebtn", g).length, 0);
});

test("Fill keeps the colour field and the theme pills; Fill's other keys are behind More", () => {
  const m = mount(section({ bgimg: "/media/a.jpg" }));
  const g = m.group("Fill");
  assert.equal(m.find((n) => n.attrs?.["aria-label"] === "Reset Background", g).length > 0, true);
  assert.equal(m.find((n) => n.className === "hm-bs-morebtn", g)[0].attrs["aria-expanded"], "true", "a set background image opens More");
});

test("the Text style select repaints the Sz and Wt fields from the batch it wrote, still one commit", () => {
  const node = heading({ fs: "16px", fw: "normal" });
  const m = mount(node);
  const field = (short) => {
    const lb = m.find((n) => n.className?.includes("hm-bs-fld__lb") && n.kids[0]?.textContent === short)[0];
    return m.find((n) => n.attrs?.id === lb.attrs.for && n.tag !== "label")[0];
  };
  const sel = m.find((n) => n.tag === "select" && n.kids.some((o) => o.textContent === "Custom"), m.group("Typography"))[0];
  assert.equal(field("Sz").value, "16");
  sel.value = "2xl";
  sel.fire("change");
  assert.equal(m.calls.length, 1, "one batch");
  assert.notEqual(field("Sz").value, "16", "the size field shows the preset, so a scrub starts from it");
  const wt = m.find((n) => n.tag === "select" && n.kids.some((o) => o.attrs?.value === "bold") && n.value === "bold", m.group("Typography"));
  assert.equal(wt.length, 1, "the weight control shows bold");
});

test("Align reaches every ta value: Justify writes justify and shows pressed when ta is justify", () => {
  const m = mount(heading(), { parent: section() });
  press(m, "Justify", "Typography").fire("click");
  assert.deepEqual(m.calls.at(-1), { key: "ta", value: "justify", at: "base", opts: { label: "Change Text align" } });
  const on = mount(heading({ ta: "justify" }), { parent: section() });
  assert.equal(press(on, "Justify", "Typography").attrs["aria-pressed"], "true");
  assert.equal(press(on, "Left", "Typography").attrs["aria-pressed"], "false");
});
