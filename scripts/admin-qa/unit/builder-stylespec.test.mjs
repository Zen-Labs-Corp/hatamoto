// The shared Go/JS fixture (backend/testdata/stylespec): the served table and the cases the Go
// validator (builder.styleDecl) is tested with. The client preview (host.js styleCSS) must accept
// exactly the same values, and give the same css where it computes one.
// Run: node --test scripts/admin-qa/unit/builder-stylespec.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { styleCSS, styleDecls } = await import(new URL("../../../backend/web/admin/js/canvas/host.js", import.meta.url));
const stylePure = await import(new URL("../../../backend/web/admin/js/builder/style.js", import.meta.url));
const { control } = stylePure;

const fixture = (f) => JSON.parse(readFileSync(new URL(`../../../backend/testdata/stylespec/${f}`, import.meta.url), "utf8"));
const table = fixture("table.json");
const cases = fixture("cases.json");
const by = new Map(table.map((e) => [e.key, e]));
// The server composes these (gradient + image layers, minmax tracks, url()): the client only checks them.
const COMPOSED = new Set(["grad", "media", "gtc"]);

test("fixture: the client accepts exactly the values the Go validator accepts", () => {
  assert.ok(cases.length > 200);
  for (const c of cases) {
    const entry = by.get(c.key);
    assert.ok(entry, `${c.key} is in the table`);
    const css = styleCSS(entry, c.value);
    assert.equal(css !== null, c.ok, `${c.key}=${JSON.stringify(c.value)}`);
    if (c.ok && !COMPOSED.has(entry.kind)) assert.equal(css, c.css, `${c.key}=${JSON.stringify(c.value)} css`);
  }
});

test("fixture: settle keys are exactly the server-settled ones", () => {
  assert.deepEqual(table.filter((e) => e.settle).map((e) => e.key).sort(), [
    "bblur", "bgimg", "blur", "bright", "cols", "contrast", "display", "grad", "gray", "gtc", "hide", "hue", "rot", "sat", "sc", "skx", "sky", "tdelay", "tdur", "tease", "trans", "tx", "ty",
  ]);
});

test("fixture: count keys carry min and max, z starts at 0, order at -1, and neither gets 1..4 presets", () => {
  for (const e of table.filter((x) => x.kind === "count")) assert.ok(Number.isInteger(e.min) && Number.isInteger(e.max), e.key);
  assert.deepEqual([by.get("z").min, by.get("order").min], [0, -1]);
  for (const k of ["z", "order"]) assert.deepEqual(control(by.get(k)).options, [], k);
  assert.equal(control(by.get("cs")).options.length, 4);
});

test("preview decls: settle keys give none, other keys one per property, a refused value null", () => {
  for (const e of table.filter((x) => x.settle)) assert.deepEqual(styleDecls(e, null), [], e.key);
  assert.deepEqual(styleDecls(by.get("display"), "flex"), [], "settle: the server's css alone shows it");
  assert.deepEqual(styleDecls(by.get("pt"), "1rem"), [["padding-top", "1rem"]]);
  assert.deepEqual(styleDecls(by.get("pt"), null), [["padding-top", null]]);
  assert.equal(styleDecls(by.get("pt"), "red"), null);
});

test("fixture: only visual keys may sit in a state; layout, spacing and transition keys may not", () => {
  const on = table.filter((e) => e.states).map((e) => e.key);
  for (const k of ["bg", "color", "bc", "grad", "op", "shadow", "radius", "bw", "bs", "td", "fw", "tx", "ty", "sc", "rot", "skx", "sky", "torigin", "blur", "bright", "contrast", "sat", "gray", "hue", "bblur", "blend"]) {
    assert.ok(on.includes(k), `${k} allows states`);
  }
  for (const k of ["display", "hide", "pos", "top", "cols", "gap", "order", "w", "h", "pt", "mt", "fs", "bgimg", "trans", "tdur", "tease", "tdelay"]) {
    assert.ok(!on.includes(k), `${k} does not allow states`);
  }
});

test("fixture: compiled state cases agree with the client table", () => {
  const states = fixture("states.json");
  assert.ok(states.length >= 6);
  for (const c of states) {
    for (const [at, m] of Object.entries(c.s)) {
      if (!at.includes(":")) continue;
      for (const [k, v] of Object.entries(m)) {
        const e = by.get(k);
        const css = styleCSS(e, v);
        assert.notEqual(css, null, `${c.name}: ${k}=${v} is a valid value`);
        // A single-declaration key shows its declaration when the table allows it in a state, and never when it does not.
        const decl = `${e.props[0]}:${css};`;
        if (e.settle) continue;
        assert.equal(c.css.includes(decl), Boolean(e.states) && !(c.name.includes("without a parent")), `${c.name}: ${k} ${decl}`);
      }
    }
    assert.ok(!c.css.includes("data-hm-force"), c.name);
    for (const part of c.css.split("@media").slice(1)) {
      if (part.includes("transition-")) assert.ok(part.includes("prefers-reduced-motion:no-preference"), `${c.name}: ${part}`);
    }
  }
});

test("fixture: with the real table a state lists no layout, spacing or transition keys", () => {
  const { groupsFor } = stylePure;
  const keys = groupsFor(table, { id: "a", t: "container" }, { id: "p", t: "container" }, "hover").flatMap((g) => g.entries.map((e) => e.key));
  assert.ok(keys.includes("ty") && keys.includes("shadow") && keys.includes("bg"));
  for (const k of ["gap", "pt", "mt", "w", "display", "trans", "tdur", "hide"]) assert.ok(!keys.includes(k), k);
  const normal = groupsFor(table, { id: "a", t: "container" }).flatMap((g) => g.entries.map((e) => e.key));
  assert.ok(["trans", "tdur", "tease", "tdelay", "gap"].every((k) => normal.includes(k)));
});
