// Navigator field rows: a section whose component has no blocks lists its non-empty content fields as presentation-only rows.
// Run: node --test scripts/admin-qa/unit/builder-navigator-fields.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);

// A just-enough DOM: attributes, children, deep clone, a CSSOM stub that records setProperty.
function el(tag) {
  const e = {
    tag, attrs: {}, kids: [], isConnected: true, value: "", disabled: false, listeners: {}, props: {}, cssText: undefined,
    style: { setProperty(k, v) { e.props[k] = v; } },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] ?? null; },
    append(...k) { this.kids.push(...k); },
    prepend(...k) { this.kids.unshift(...k); },
    replaceChildren(...k) { this.kids = k; },
    set textContent(v) { this.kids = [String(v)]; },
    addEventListener(t, f) { this.listeners[t] = f; },
    cloneNode() { const c = el(this.tag); c.attrs = { ...this.attrs }; return c; },
    focus() {}, select() {}, contains: () => false,
    getBoundingClientRect: () => ({ left: 10, bottom: 20 }),
    get text() { return this.kids.map((k) => (typeof k === "string" ? k : k.text)).join(""); },
  };
  return e;
}
const all = (e, out = []) => (out.push(e), e.kids.forEach((k) => typeof k !== "string" && all(k, out)), out);
const withAttr = (e, a) => all(e).filter((x) => a in x.attrs);
const withClass = (e, c) => all(e).filter((x) => (x.attrs.class ?? "").split(" ").includes(c));
globalThis.document = { createElement: el, activeElement: null };
globalThis.requestAnimationFrame = (f) => (f(), 0);
const { mountNavigator, rowsOf, fieldRows, step, collapseAll, allCollapsed, metaOf } = await import(new URL("builder/navigator.js", root));


const FIELDS = [
  { key: "headline", label: "Headline", kind: "text" },
  { key: "body", label: "Body", kind: "richtext" },
  { key: "cta", label: "Button link", kind: "link" },
  { key: "cards", label: "Cards", kind: "list" },
  { key: "empty", label: "Empty", kind: "text" },
];
const content = { headline: "Build pages", body: "<p>Hello   <b>there</b></p>", cards: [1, 2, 3], empty: "", cta: "https://example.com/" + "x".repeat(60) };
const comp = (id, c, p = {}) => ({ id, t: "component", p: { type: "hero_banner", content, ...p }, ...(c ? { c } : {}) });
const sec = (id, c, s) => ({ id, t: "container", p: { tag: "section" }, c, ...(s ? { s } : {}) });
const head = (id, text) => ({ id, t: "heading", p: { text } });
const sections = [{ type: "hero_banner", title: "Hero banner", fields: FIELDS }];
const doc = { v: 2, nodes: [sec("s1", [comp("c1")], { base: { bg: "#102030" } }), sec("s2", [comp("c2", [head("h", "Block")])]), sec("s3", [comp("c3", undefined, { content: {} })])] };
const fieldsOf = (c) => fieldRows(c.p?.content, sections.find((s) => s.type === c.p?.type)?.fields);
const rows = (o = {}) => rowsOf(o.d ?? doc, o.collapsed ?? new Set(), o.filter ?? "", undefined, fieldsOf);

test("fieldRows keeps non-empty fields in order: HTML stripped, spaces collapsed, 40 chars, lists as N items", () => {
  const r = fieldRows(content, FIELDS);
  assert.deepEqual(r.map((f) => f.key), ["headline", "body", "cta", "cards"]);
  assert.equal(r[0].text, "Build pages");
  assert.equal(r[1].text, "Hello there");
  assert.equal(r[2].text.length, 40);
  assert.equal(r[3].text, "3 items");
  assert.deepEqual(r[1], { key: "body", label: "Body", kind: "richtext", text: "Hello there" });
});

test("fieldRows: null, empty list, empty object and a tag-only value give no row; an object shows its first string", () => {
  const f = ["a", "b", "c", "d", "e", "g"].map((key) => ({ key, label: key, kind: "text" }));
  const r = fieldRows({ a: null, b: [], c: {}, d: "<br>", e: { n: 1, title: "Hi <i>x</i>" }, g: { n: 1 } }, f);
  assert.deepEqual(r.map((x) => [x.key, x.text]), [["e", "Hi x"]]);
  assert.deepEqual(fieldRows(undefined, f), []);
  assert.deepEqual(fieldRows({ a: "x" }, undefined), []);
});

test("rowsOf: a collapsed component without blocks lists field rows in order, level 2, id <container>#<key>", () => {
  const r = rows();
  assert.deepEqual(r.map((x) => x.id), ["s1", "s1#headline", "s1#body", "s1#cta", "s1#cards", "s2", "h", "s3"]);
  const s1 = r[0];
  assert.equal(s1.kids, 4);
  assert.equal(s1.expanded, true);
  const f = r[1];
  assert.equal(f.level, 2);
  assert.equal(f.parentId, "s1");
  assert.equal(f.kids, 0);
  assert.equal(f.section, false);
  assert.equal(f.collapsed, null);
  assert.deepEqual([f.pos, f.size], [1, 4]);
  assert.deepEqual(f.field, { ...FIELDS[0], text: "Build pages", componentId: "c1" });
  assert.equal(r.find((x) => x.id === "s2").kids, 1, "a component with blocks keeps its blocks, no field rows");
  assert.equal(r.find((x) => x.id === "s3").kids, 0, "no non-empty field: no caret");
});

test("a field id can never be a node id", () => {
  const idRe = /^[A-Za-z0-9_-]{1,64}$/;
  const ids = rows().map((x) => x.id);
  assert.ok(ids.filter((i) => i.includes("#")).length === 4);
  ids.filter((i) => i.includes("#")).forEach((i) => assert.equal(idRe.test(i), false));
});

test("step moves across field and section rows; Left returns to the section", () => {
  const r = rows();
  assert.deepEqual(step(r, "s1", "next"), { select: "s1#headline" });
  assert.deepEqual(step(r, "s1#headline", "prev"), { select: "s1" });
  assert.deepEqual(step(r, "s1#cards", "next"), { select: "s2" });
  assert.deepEqual(step(r, "s2", "prev"), { select: "s1#cards" });
  assert.deepEqual(step(r, "s1#body", "first"), { select: "s1" });
  assert.deepEqual(step(r, "s1#body", "last"), { select: "s3" });
  assert.deepEqual(step(r, "s1#body", "collapse"), { select: "s1" });
  assert.equal(step(r, "s1#body", "expand"), null);
  assert.deepEqual(step(r, "s1", "collapse"), { collapse: "s1" });
  assert.deepEqual(step(rows({ collapsed: new Set(["s1"]) }), "s1", "expand"), { expand: "s1" });
  assert.deepEqual(step(r, "s1", "expand"), { select: "s1#headline" });
});

test("collapse all / allCollapsed / caret count field kids", () => {
  assert.deepEqual([...collapseAll(doc, fieldsOf)].sort(), ["s1", "s2"]);
  assert.deepEqual([...collapseAll(doc)], ["s2"], "without fieldsOf only blocks count");
  assert.equal(allCollapsed(doc, new Set(["s2"]), fieldsOf), false);
  assert.equal(allCollapsed(doc, new Set(["s1", "s2"]), fieldsOf), true);
  assert.deepEqual(rows({ collapsed: new Set(["s1"]) }).map((x) => x.id).slice(0, 2), ["s1", "s2"]);
  assert.equal(rows({ collapsed: new Set(["s1"]) })[0].expanded, false);
  assert.equal(rows({ collapsed: new Set(["s1"]) })[0].kids, 4);
});

test("filter matches a field's text or label and keeps its section; non-matching fields hide", () => {
  assert.deepEqual(rows({ filter: "build" }).map((x) => x.id), ["s1", "s1#headline"]);
  assert.deepEqual(rows({ filter: "button link" }).map((x) => x.id), ["s1", "s1#cta"]);
  assert.deepEqual(rows({ filter: "3 items" }).map((x) => x.id), ["s1", "s1#cards"]);
  assert.deepEqual(rows({ filter: "zzz" }), []);
  assert.deepEqual(rows({ filter: "hero" }).map((x) => x.id), ["s1", "s2", "h", "s3"].filter((i) => i !== "h"), "a section-name match keeps the sections, not their fields");
});

test("metaOf counts field rows when the component has no blocks", () => {
  const r = rows();
  assert.equal(metaOf(r[0]), "4 blocks · #102030");
  assert.equal(metaOf(r.find((x) => x.id === "s2")), "1 block");
  assert.equal(metaOf(r.find((x) => x.id === "s3")), "");
  assert.equal(metaOf(r[1]), "");
});

// --- mounted: paint and handlers ---
function mount({ d = doc, selected = null } = {}) {
  const painted = [];
  const icons = { querySelector: (q) => { const m = /data-ico="([^"]+)"/.exec(q); return m ? Object.assign(el("span"), { attrs: { "data-ico": m[1] } }) : null; } };
  const rootEl = {
    closest: () => ({ querySelector: (q) => (q.includes("data-hm-bs-ico") ? { content: icons } : null) }),
    contains: () => false, replaceChildren: (...k) => painted.push(k), listeners: {}, addEventListener(t, f) { this.listeners[t] = f; },
    querySelector: () => null, querySelectorAll: () => [],
  };
  const calls = [];
  const spy = (n) => (...a) => calls.push([n, ...a]);
  const warn = console.warn;
  console.warn = () => {};
  const tree = mountNavigator(rootEl, {
    getDoc: () => d, getSelected: () => selected, onSelect: spy("select"), onToggleHide: spy("hide"), onMenu: spy("menu"), onRename: spy("rename"), onAddAt: spy("addAt"),
    onField: spy("field"), getSections: () => sections,
  });
  console.warn = warn;
  tree.draw();
  return { tree, rootEl, calls, rows: () => painted.at(-1)[0].kids };
}
// An event whose target is a field row (dataset as the browser exposes data-* attributes).
const fieldRow = { dataset: { id: "s1#body", field: "body", comp: "c1" } };
const hit = (row, extra = {}) => ({ detail: 1, target: { closest: (q) => (q === "[data-id]" ? row : null), dataset: row.dataset }, ...extra });

test("paint: a field row is a level-2 treeitem named 'Label · text' with a muted label, a kind icon and no eye/more", () => {
  const li = mount().rows().find((r) => r.attrs["data-id"] === "s1#body");
  assert.equal(li.attrs.role, "treeitem");
  assert.equal(li.attrs["aria-level"], "2");
  assert.equal(li.attrs["data-field"], "body");
  assert.equal(li.attrs["data-comp"], "c1");
  assert.equal(withClass(li, "hm-bs-row__name")[0].text, "Body · Hello there");
  assert.equal(withClass(li, "hm-bs-row__kind")[0].text, "Body");
  assert.equal(withClass(li, "hm-bs-row__kind")[0].attrs["aria-hidden"], "true");
  assert.equal(withAttr(li, "data-ico")[0].attrs["data-ico"], "heading");
  assert.equal(withClass(li, "hm-bs-row__acts").length, 0);
  const icos = Object.fromEntries(mount().rows().filter((r) => r.attrs["data-field"]).map((r) => [r.attrs["data-field"], withAttr(r, "data-ico")[0].attrs["data-ico"]]));
  assert.deepEqual(icos, { headline: "heading", body: "heading", cta: "link", cards: "list" });
  assert.equal(mount().rows()[0].attrs["aria-expanded"], "true");
  assert.equal(withClass(mount().rows()[0], "hm-bs-row__meta")[0].text, "4 blocks · #102030");
});

test("click and Enter/Space on a field row call onField(componentId, key) and nothing else", () => {
  const m = mount();
  m.rootEl.listeners.click(hit(fieldRow));
  const stop = { preventDefault() {}, stopPropagation() {} };
  assert.deepEqual(m.calls, [["field", "c1", "body"]]);
  const row = { dataset: fieldRow.dataset };
  row.closest = () => row;
  m.rootEl.listeners.keydown({ key: "Enter", target: row, ...stop });
  m.rootEl.listeners.keydown({ key: " ", target: row, ...stop });
  assert.deepEqual(m.calls.map((c) => c[0]), ["field", "field", "field"]);
  assert.deepEqual(m.calls.at(-1), ["field", "c1", "body"]);
});

test("a field id never reaches onSelect, onToggleHide, onRename or onMenu with '#'", () => {
  const m = mount();
  const row = { dataset: fieldRow.dataset, closest: () => row };
  const stop = { preventDefault() {}, stopPropagation() {} };
  m.rootEl.listeners.click(hit(row));
  m.rootEl.listeners.click({ detail: 2, target: { closest: (q) => (q === "[data-id]" ? row : null), dataset: row.dataset } });
  m.rootEl.listeners.dblclick({ target: { closest: (q) => (q === "[data-id]" ? row : null) } });
  m.rootEl.listeners.keydown({ key: "h", target: row, ...stop });
  m.rootEl.listeners.contextmenu({ target: { closest: (q) => (q === "[data-id]" ? row : null) }, clientX: 3, clientY: 4, ...stop });
  assert.deepEqual(m.calls.filter((c) => c[0] !== "field"), [["menu", "s1", 3, 4]], "the menu acts on the section");
  assert.ok(m.calls.flat().every((a) => typeof a !== "string" || !a.includes("#")));
  assert.equal(m.calls.filter((c) => c[0] === "field").length, 2);
});

test("act: from a focused field row, keys start there; onto a field row it focuses without selecting", () => {
  const m = mount({ selected: "s1" });
  m.rootEl.contains = () => true;
  document.activeElement = { dataset: { id: "s1#body", field: "body" } };
  assert.equal(m.tree.act("next"), true);
  assert.deepEqual(m.calls.filter((c) => c[0] === "select"), [], "next field row: no selection");
  document.activeElement = { dataset: { id: "s1#cards", field: "cards" } };
  m.tree.act("next");
  assert.deepEqual(m.calls.filter((c) => c[0] === "select"), [["select", "s2"]], "onto the next section selects it");
  m.calls.length = 0;
  document.activeElement = { dataset: { id: "s1#body", field: "body" } };
  m.tree.act("collapse");
  assert.deepEqual(m.calls, [], "Left back onto the section already selected: repaint, no select");
  assert.deepEqual(m.calls.filter((c) => c[0] === "field"), []);
  document.activeElement = null;
});

test("screen.js: the drag pick skips field rows and onField opens the Inspector field", () => {
  const src = readFileSync(new URL("../../../backend/web/admin/js/builder/screen.js", import.meta.url), "utf8");
  assert.match(src, /closest\?\.\("[^"]*\[data-field\][^"]*"\) \? null/);
  assert.match(src, /onField: \(id, key\) => toField\(id, key\)/);
});
