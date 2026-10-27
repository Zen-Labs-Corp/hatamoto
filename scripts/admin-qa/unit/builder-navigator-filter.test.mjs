// Navigator toolbar: the filter keeps matches and their ancestors, Collapse all / expand, Rename needs a selection.
// Run: node --test scripts/admin-qa/unit/builder-navigator-filter.test.mjs
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

function el(tag) {
  return {
    tag, attrs: {}, kids: [], isConnected: true, style: { setProperty() {} }, value: "", disabled: false, listeners: {},
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...k) { this.kids.push(...k); },
    prepend(...k) { this.kids.unshift(...k); },
    replaceChildren(...k) { this.kids = k; },
    set textContent(v) { this.kids = [String(v)]; },
    addEventListener(t, f) { this.listeners[t] = f; },
    focus() {}, select() {}, contains: () => false,
  };
}
globalThis.document = { createElement: el, activeElement: null };
globalThis.requestAnimationFrame = (f) => (f(), 0);
const { mountNavigator, rowsOf, collapseAll } = await import(new URL("builder/navigator.js", root));

const h = (id, text) => ({ id, t: "heading", p: { text } });
const doc = { v: 2, nodes: [{ id: "s", t: "container", p: { tag: "section" }, c: [{ id: "b", t: "container", c: [h("h1", "Build pages"), h("h2", "Other")] }] }, h("h3", "Build fast")] };

test("filter keeps matches and their ancestors, ignoring collapsed", () => {
  const rows = rowsOf(doc, new Set(["s", "b"]), "build");
  assert.deepEqual(rows.map((r) => r.id), ["s", "b", "h1", "h3"]);
  assert.deepEqual(rows.filter((r) => r.match).map((r) => r.id), ["h1", "h3"]);
  assert.equal(rows[0].expanded, true);
});

test("every word must match, case-insensitively", () => {
  assert.deepEqual(rowsOf(doc, new Set(), "BUILD pages").map((r) => r.id), ["s", "b", "h1"]);
  assert.deepEqual(rowsOf(doc, new Set(), "zzz"), []);
});

test("no filter is the plain tree", () => {
  assert.deepEqual(rowsOf(doc, new Set(["b"]), "").map((r) => r.id), ["s", "b", "h3"]);
});

test("collapseAll lists the ids with children", () => {
  assert.deepEqual([...collapseAll(doc)].sort(), ["b", "s"]);
});

function mount(selected) {
  const input = el("input");
  const collapse = el("button");
  collapse.kids = [el("span")];
  collapse.querySelector = () => collapse.kids[0];
  const rename = el("button");
  const bar = { querySelector: (q) => ({ "[data-hm-bs-filter]": input, "[data-hm-bs-collapse]": collapse, "[data-hm-bs-rename]": rename })[q] };
  const painted = [];
  const said = [];
  const rootEl = { closest: () => ({ querySelector: () => null }), contains: () => false, replaceChildren: (...k) => painted.push(k), listeners: {}, addEventListener(t, f) { this.listeners[t] = f; }, querySelector: () => null, querySelectorAll: () => [] };
  const picks = [];
  const warn = console.warn;
  console.warn = () => {};
  const tree = mountNavigator(rootEl, { getDoc: () => doc, getSelected: () => selected, onSelect: (id, mods) => picks.push([id, mods]), bar, announce: (m) => said.push(m) });
  console.warn = warn;
  return { tree, input, collapse, rename, painted, said, picks, rootEl };
}

const rowCount = (painted) => painted.at(-1)[0].kids.length;

test("Rename is disabled without a selection", () => {
  assert.equal((() => { const m = mount(null); m.tree.draw(); return m.rename.disabled; })(), true);
  assert.equal((() => { const m = mount("h1"); m.tree.draw(); return m.rename.disabled; })(), false);
});

test("typing filters the tree, announces the count, Escape clears", async () => {
  const m = mount("h1");
  m.tree.draw();
  assert.equal(rowCount(m.painted), 5);
  m.input.value = "build";
  m.input.listeners.input();
  assert.equal(rowCount(m.painted), 4);
  await new Promise((r) => setTimeout(r, 450));
  assert.deepEqual(m.said, ["2 matches"]);
  m.input.listeners.keydown({ key: "Escape", stopPropagation() {} });
  assert.equal(m.input.value, "");
  assert.equal(rowCount(m.painted), 5);
  m.input.value = "zzz";
  m.input.listeners.input();
  assert.match(m.painted.at(-1)[0].kids[0], /No elements match/);
});

test("Collapse all collapses every parent, then expands them again", () => {
  const m = mount("h1");
  m.tree.draw();
  m.collapse.listeners.click();
  assert.equal(rowCount(m.painted), 2);
  assert.equal(m.collapse.kids[0].kids[0], "Expand all");
  m.collapse.listeners.click();
  assert.equal(rowCount(m.painted), 5);
});

test("only a plain click or tap is a pick; modifier clicks, a double-click's second click and arrow keys are not", () => {
  const m = mount("h1");
  const click = (o) => m.rootEl.listeners.click({ target: { closest: (q) => (q === "[data-id]" ? { dataset: { id: "h2" } } : null) }, ...o });
  click({ detail: 1 });
  click({ detail: 1, shiftKey: true });
  click({ detail: 1, ctrlKey: true });
  click({ detail: 1, metaKey: true });
  click({ detail: 2 });
  assert.deepEqual(m.picks.map(([, mods]) => mods.pick), [true, false, false, false, false]);
  m.tree.act("next"); // arrow keys select without a pick
  assert.equal(m.picks.at(-1)[1], undefined);
});

test("Collapse all collapses everything while any parent is open; the label flips only when all are collapsed", () => {
  const m = mount("h1");
  m.tree.draw();
  const label = () => m.collapse.kids[0].kids[0];
  m.rootEl.listeners.click({ target: { closest: (q) => (q === "[data-caret]" ? {} : q === "[data-id]" ? { dataset: { id: "b" } } : null) } }); // one caret: "b" collapses
  assert.equal(label(), "Collapse all", "one collapsed row is not all collapsed");
  m.collapse.listeners.click();
  assert.equal(rowCount(m.painted), 2, "collapses everything, not expands");
  assert.equal(label(), "Expand all");
  m.collapse.listeners.click();
  assert.equal(rowCount(m.painted), 5);
  assert.equal(label(), "Collapse all");
});

test("a theme section reads the same everywhere: a plain title-case name, with or without the sections table", async () => {
  const { label, useLabels } = await import(new URL("builder/navigator.js", root));
  const comp = (type) => ({ id: "c", t: "component", p: { type } });
  assert.equal(label(comp("features")), "Features", "not 'features section'");
  assert.equal(label(comp("card_columns")), "Card columns");
  assert.equal(label(comp("")), "Section");
  useLabels({ sections: [{ type: "features", title: "Our features" }] });
  assert.equal(label(comp("features")), "Our features", "a bare label() (the Inspector header) uses the registered titles");
  assert.equal(label(comp("features"), undefined, undefined, []), "Features");
  useLabels();
});

test("a row does not repeat its kind name when the text is just that name", async () => {
  const { label } = await import(new URL("builder/navigator.js", root));
  assert.equal(label(h("a", "Heading")), "Heading");
  assert.equal(label(h("a", "Build pages")), "Heading · Build pages");
});
