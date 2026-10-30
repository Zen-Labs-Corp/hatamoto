// Navigator paint: a section is one numbered row, blocks are icon rows, the hover controls are inert mouse affordances.
// Run: node --test scripts/admin-qa/unit/builder-navigator-paint.test.mjs
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
const { mountNavigator } = await import(new URL("builder/navigator.js", root));

const head = (id, text) => ({ id, t: "heading", p: { text } });
const comp = (id, c) => ({ id, t: "component", p: { type: "hero_banner" }, ...(c ? { c } : {}) });
const sec = (id, c, s) => ({ id, t: "container", p: { tag: "section" }, c, ...(s ? { s } : {}) });
const doc = { v: 2, nodes: [sec("s1", [comp("c1", [head("h1", "Hello there")])], { base: { bg: "#102030", ta: "center" } }), sec("s2", [comp("c2")]), sec("s3", [head("x", "X"), head("y", "Y")])] };

function mount({ selected = null, filterText = "", d = doc } = {}) {
  const painted = [];
  const icons = { querySelector: (q) => { const m = /data-ico="([^"]+)"/.exec(q); return m ? Object.assign(el("span"), { attrs: { "data-ico": m[1] } }) : null; } };
  const tpl = { content: icons };
  const rootEl = {
    closest: () => ({ querySelector: (q) => (q.includes("data-hm-bs-ico") ? tpl : null) }),
    contains: () => false, replaceChildren: (...k) => painted.push(k), listeners: {}, addEventListener(t, f) { this.listeners[t] = f; },
    querySelector: () => null, querySelectorAll: () => [],
  };
  const calls = [];
  const input = el("input");
  const bar = { querySelector: (q) => (q === "[data-hm-bs-filter]" ? input : null) };
  const warn = console.warn;
  console.warn = () => {};
  const tree = mountNavigator(rootEl, {
    getDoc: () => d, getSelected: () => selected, onSelect: (...a) => calls.push(["select", ...a]),
    onToggleHide: (...a) => calls.push(["hide", ...a]), onMenu: (...a) => calls.push(["menu", ...a]), onAddAt: (...a) => calls.push(["addAt", ...a]),
    bar, getSections: () => [{ type: "hero_banner", title: "Hero banner" }],
  });
  console.warn = warn;
  tree.draw();
  if (filterText) (input.value = filterText), input.listeners.input();
  const ul = () => painted.at(-1)[0];
  return { tree, rootEl, calls, ul, rows: () => ul().kids };
}
const click = (m, hit, extra = {}) => m.rootEl.listeners.click({ detail: 1, target: { closest: (q) => hit[q] ?? null, dataset: {} }, ...extra });

test("a collapsed section paints one numbered row with its name and meta; the ul holds only li", () => {
  const m = mount();
  const rows = m.rows();
  assert.deepEqual(rows.map((r) => r.attrs["data-id"]), ["s1", "h1", "s2", "s3", "x", "y"]);
  assert.ok(rows.every((r) => r.tag === "li"));
  const first = rows[0];
  assert.equal(first.attrs["aria-level"], "1");
  assert.equal(first.attrs.role, "treeitem");
  assert.match(first.attrs.class, /hm-bs-row--section/);
  assert.equal(withClass(first, "hm-bs-row__idx")[0].text, "01");
  assert.equal(withClass(first, "hm-bs-row__name")[0].text, "Hero banner");
  assert.equal(withClass(first, "hm-bs-row__meta")[0].text, "1 block · #102030");
  assert.equal(withClass(rows[2], "hm-bs-row__idx")[0].text, "02");
  assert.equal(withClass(rows[3], "hm-bs-row__idx")[0].text, "03");
});

test("a block row: icon, opening text and muted kind, indented as level 2", () => {
  const b = mount().rows()[1];
  assert.equal(b.attrs["aria-level"], "2");
  assert.match(b.attrs.class, /hm-bs-row--block/);
  assert.equal(withClass(b, "hm-bs-row__ico")[0].attrs["aria-hidden"], "true");
  assert.equal(withAttr(b, "data-ico")[0].attrs["data-ico"], "heading");
  assert.equal(withClass(b, "hm-bs-row__name")[0].text, "Hello there");
  assert.equal(withClass(b, "hm-bs-row__kind")[0].text, "Heading");
});

test("selecting the component from the canvas selects its collapsed row and takes the tab stop", () => {
  const rows = mount({ selected: "c2" }).rows();
  assert.equal(rows[2].attrs["aria-selected"], "true");
  assert.equal(rows[2].attrs.tabindex, "0");
  assert.deepEqual(rows.map((r) => r.attrs["aria-selected"]), ["false", "false", "true", "false", "false", "false"]);
  assert.equal(rows.filter((r) => r.attrs.tabindex === "0").length, 1);
});

test("a section holding the selection gets the band, with its blocks; the selected row does not", () => {
  const rows = mount({ selected: "h1" }).rows();
  assert.match(rows[0].attrs.class, /hm-bs-row--band/);
  assert.doesNotMatch(rows[1].attrs.class, /hm-bs-row--band/); // the selected block itself
  assert.doesNotMatch(rows[2].attrs.class, /hm-bs-row--band/);
  const own = mount({ selected: "s1" }).rows();
  assert.doesNotMatch(own[0].attrs.class, /hm-bs-row--band/);
  assert.doesNotMatch(own[1].attrs.class, /hm-bs-row--band/); // the row itself is solid; no band around it
});

test("clicking the collapsed row selects the container with pick", () => {
  const m = mount();
  click(m, { "[data-id]": { dataset: { id: "s1" } } }, { shiftKey: false, ctrlKey: false, metaKey: false, altKey: false });
  assert.deepEqual(m.calls, [["select", "s1", { shift: false, ctrl: false, pick: true }]]);
});

test("eye and more act on the row; neither selects", () => {
  const m = mount();
  const row = { dataset: { id: "s2" } };
  const act = { getBoundingClientRect: () => ({ left: 5, bottom: 9 }) };
  click(m, { "[data-hm-bs-eye]": act, "[data-id]": row });
  click(m, { "[data-hm-bs-more]": act, "[data-id]": row });
  assert.deepEqual(m.calls, [["hide", "s2"], ["menu", "s2", 5, 9]]);
});

test("the gap calls onAddAt with the section index; the last row adds the trailing one", () => {
  const m = mount();
  const gaps = withAttr(m.ul(), "data-hm-bs-gap");
  assert.deepEqual(gaps.map((g) => g.attrs["data-hm-bs-gap"]), ["0", "1", "2", "3"]);
  assert.equal(m.rows().at(-1).kids.at(-1), gaps.at(-1), "trailing gap sits at the foot of the last row");
  click(m, { "[data-hm-bs-gap]": gaps[3] });
  assert.deepEqual(m.calls, [["addAt", 3, gaps[3]]]);
  assert.match(gaps[0].text, /Add section here/);
});

test("no gap while a filter is active", () => {
  const m = mount({ filterText: "hero" });
  assert.equal(withAttr(m.ul(), "data-hm-bs-gap").length, 0);
  assert.deepEqual(m.rows().map((r) => r.attrs["data-id"]), ["s1", "s2"]); // both match the component's name; the index stays
  assert.equal(withClass(m.rows()[1], "hm-bs-row__idx")[0].text, "02");
});

test("eye, more and gap carry no data-id, role or tabindex and are aria-hidden", () => {
  const m = mount();
  const inert = [...withAttr(m.ul(), "data-hm-bs-eye"), ...withAttr(m.ul(), "data-hm-bs-more"), ...withAttr(m.ul(), "data-hm-bs-gap")];
  assert.ok(inert.length >= 6);
  for (const e of inert) for (const a of ["data-id", "role", "tabindex"]) assert.equal(a in e.attrs, false, `${a} on ${Object.keys(e.attrs)}`);
  for (const box of withClass(m.ul(), "hm-bs-row__acts")) assert.equal(box.attrs["aria-hidden"], "true");
  for (const g of withAttr(m.ul(), "data-hm-bs-gap")) assert.equal(g.attrs["aria-hidden"], "true");
});

test("the thumbnail is set through setProperty only", () => {
  const rows = mount().rows();
  const t = withClass(rows[0], "hm-bs-row__thumb")[0];
  assert.equal(t.attrs["data-align"], "center");
  assert.deepEqual(t.props, { "--bs-thumb": "#102030", "--bs-thumb-fg": "white" });
  assert.equal("style" in t.attrs, false);
  assert.equal(t.cssText, undefined);
  assert.deepEqual(withClass(rows[2], "hm-bs-row__thumb")[0].props, {});
  assert.equal(all({ kids: rows, attrs: {} }).some((x) => "style" in x.attrs), false);
});

test("a hidden section shows the open eye-off icon and the hidden class", () => {
  const d = { v: 2, nodes: [sec("s", [comp("c")], { base: { hide: true } })] };
  const row = mount({ d }).rows()[0];
  assert.match(row.attrs.class, /hm-bs-row--hidden/);
  assert.deepEqual(withAttr(row, "data-ico").map((e) => e.attrs["data-ico"]).filter((n) => n.startsWith("eye")), ["eye-off"]);
  assert.match(withClass(row, "hm-bs-row__meta")[0]?.text ?? "Hidden", /Hidden/);
});

test("a collapsed row whose component alone is hidden shows hidden: class, badge and the eye-off icon", () => {
  const hiddenComp = { ...comp("c1"), s: { base: { hide: true } } };
  const m = mount({ d: { v: 2, nodes: [sec("s1", [hiddenComp]), sec("s2", [comp("c2")])] } });
  const [a, b] = m.rows();
  assert.match(a.attrs.class, /hm-bs-row--hidden/);
  assert.doesNotMatch(b.attrs.class, /hm-bs-row--hidden/);
  assert.ok(withAttr(a, "data-hm-bs-eye").some((e) => withAttr(e, "data-ico").some((i) => i.attrs["data-ico"] === "eye-off")));
  assert.match(withClass(a, "hm-bs-row__meta")[0].text, /Hidden/);
});

test("every row carries its depth as --bs-level, and every row has a caret slot (live only with blocks)", () => {
  const deep = { id: "d", t: "container", p: { tag: "div" }, c: [{ id: "g", t: "container", c: [{ id: "k", t: "container", c: [head("x", "X")] }, head("y", "Y")] }] };
  const m = mount({ d: { v: 2, nodes: [deep] } });
  const rows = m.rows();
  assert.deepEqual(rows.map((r) => [r.attrs["data-id"], r.props["--bs-level"]]), [["d", "1"], ["g", "2"], ["k", "3"], ["x", "4"], ["y", "3"]]);
  for (const r of rows) assert.equal(withClass(r, "hm-bs-row__caret").length, 1, r.attrs["data-id"]);
  assert.deepEqual(rows.map((r) => withAttr(r, "data-caret").length), [1, 1, 1, 0, 0]);
});
