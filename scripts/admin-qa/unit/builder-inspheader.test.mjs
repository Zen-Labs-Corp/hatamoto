// Inspector header: icon, name, parent link and the "..." menu (Paste disabled with a title until a style is copied).
// Run: node --test scripts/admin-qa/unit/builder-inspheader.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, dataset: {}, textContent: "", value: "", checked: false, parent: null });
    this.classList = { toggle() {}, add() {}, contains: () => false };
  }
  get isConnected() { let n = this; while (n.parent) n = n.parent; return n.top === true; }
  get firstChild() { return this.kids[0] ?? null; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { x.parent?.kids.splice(x.parent.kids.indexOf(x), 1); this.kids.push(x); x.parent = this; } }
  replaceChildren(...k) { this.kids.forEach((x) => (x.parent = null)); this.kids = []; this.append(...k); }
  remove() { if (this.parent) { this.parent.kids = this.parent.kids.filter((x) => x !== this); this.parent = null; } }
  replaceWith(n) { const p = this.parent; p.kids[p.kids.indexOf(this)] = n; n.parent = p; this.parent = null; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  hidePopover() {}
  querySelector() { return null; }
  closest() { return null; }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t), activeElement: null };
globalThis.window = globalThis;


const { header, headerModel } = await import(new URL("builder/inspheader.js", root));
const { clipboard } = await import(new URL("builder/design.js", root));

const leaf = (id, t = "heading", p = {}) => ({ id, t, p, c: [] });
const sec = (id, c) => ({ id, t: "container", p: { tag: "section" }, c });
const doc = { nodes: [sec("s1", [leaf("a"), leaf("b", "text")]), sec("s2", [])] };
const api = (extra = {}) => ({ getDoc: () => doc, select() {}, rename() {}, copyStyle() {}, pasteStyle() {}, ...extra });
const all = (el) => { const out = []; el.walk((n) => out.push(n)); return out; };
const text = (n) => n.textContent;
const sub = (el) => all(el).find((n) => n.attrs.class === "hm-bs-sub");

test("headerModel: a section counts its blocks, a block names its parent", () => {
  assert.deepEqual(headerModel({ node: doc.nodes[0], doc }), { icon: "layers", kind: "section", count: 2, parent: null });
  const m = headerModel({ node: doc.nodes[0].c[0], doc });
  assert.equal(m.kind, "block");
  assert.equal(m.icon, "heading");
  assert.equal(m.parent.id, "s1");
  assert.equal(m.parent.name, "Section");
});

test("a section header reads \"Section · N blocks\"; a block header links to its parent section", () => {
  assert.equal(text(sub(header({ node: doc.nodes[0], api: api(), name: "Section" }))), "Section · 2 blocks");
  let picked = null;
  const el = header({ node: doc.nodes[0].c[0], api: api({ select: (id) => (picked = id) }), name: "Heading" });
  assert.ok(all(sub(el)).some((n) => n.textContent === "Block in "));
  const link = all(el).find((n) => n.tag === "button" && n.attrs.class === "hm-bs-sublink");
  assert.ok(link);
  link.on.click[0]();
  assert.equal(picked, "s1", "the parent's name is a button that selects the parent section");
});

test("the name is an editable input; Enter/blur rename through the api, Escape reverts, unchanged writes nothing", () => {
  const calls = [];
  const el = header({ node: doc.nodes[0].c[0], api: api({ rename: (id, v) => calls.push([id, v]) }), name: "Heading" });
  const input = all(el).find((n) => n.tag === "input");
  assert.equal(input.attrs["aria-label"], "Name");
  input.on.blur[0]();
  assert.deepEqual(calls, [], "unchanged");
  input.value = "  Big title ";
  input.on.keydown[0]({ key: "Enter" });
  input.on.blur[0]();
  assert.deepEqual(calls, [["a", "Big title"]]);
  input.value = "Oops";
  input.on.keydown[0]({ key: "Escape" });
  assert.equal(input.value, "Heading");
});

test("the header keeps focus order icon, name, menu button", () => {
  const el = header({ node: doc.nodes[0], api: api(), name: "Section" });
  assert.deepEqual(el.kids.map((k) => k.tag), ["span", "div", "button", "div"]);
  assert.ok(!all(el).some((n) => /Editing/.test(n.textContent)), "the device chip moved to the context line");
});

test("Paste style says in its own text that a style must be copied first, and stays focusable", () => {
  clipboard.clear?.();
  const el = header({ node: { id: "a" }, api: api(), name: "Heading" });
  const paste = all(el).find((n) => n.textContent === "Paste style (copy a style first)");
  assert.ok(paste, "the reason is the item's text");
  assert.equal(paste.attrs.disabled, undefined, "a disabled item is skipped by the menu's arrow keys");
  assert.equal(paste.attrs["aria-disabled"], undefined);
  assert.ok(all(el).some((n) => n.textContent === "Copy style"));
});

test("Restore from an earlier version appears only when the screen offers it, and passes the node id", () => {
  assert.ok(!all(header({ node: { id: "a" }, api: api(), name: "H" })).some((n) => /Restore/.test(n.textContent)));
  let got = null;
  const el = header({ node: { id: "a" }, api: api({ restore: (id) => (got = id) }), name: "H" });
  const row = all(el).find((n) => /Restore from an earlier version/.test(n.textContent));
  assert.ok(row);
  row.on.click[0]();
  assert.equal(got, "a");
});
