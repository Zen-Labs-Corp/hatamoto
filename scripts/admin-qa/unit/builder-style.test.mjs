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
