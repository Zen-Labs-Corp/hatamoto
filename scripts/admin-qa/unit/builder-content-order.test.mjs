// Content tab: prop order per kind, the heading level as H1-H6, and "Use data" collapsed until opened or bound.
// Run: node --test scripts/admin-qa/unit/builder-content-order.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, dataset: {}, textContent: "", value: "", checked: false, parent: null, hidden: false });
    this.classList = { toggle() {}, add() {}, contains: () => false };
  }
  get isConnected() { return false; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { if (typeof x === "object") x.parent = this; this.kids.push(x); } }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t) { for (const f of this.on[t] ?? []) f({ target: this }); }
  querySelector(sel) { let hit = null; this.walk((n) => !hit && n !== this && n.match(sel) && (hit = n)); return hit; }
  match(sel) { const m = /^\.([\w-]+)$|^select$|^\[data-prop="(\w+)"\]$/.exec(sel); return !m ? false : m[1] ? (this.attrs.class ?? "").split(" ").includes(m[1]) : m[2] ? this.dataset.prop === m[2] : this.tag === "select"; }
  closest() { return null; }
  focus() {}
  walk(f) { f(this); this.kids.forEach((k) => typeof k === "object" && k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t), activeElement: null };
globalThis.window = globalThis;

const { createContent, orderProps } = await import(new URL("builder/inspcontent.js", root));

const P = (key, extra = {}) => ({ key, type: "string", ...extra });
const kinds = [
  { kind: "button", props: [P("href"), P("text"), P("variant")] },
  { kind: "image", props: [P("alt"), P("lightbox", { type: "boolean" }), P("src"), P("href"), P("caption")] },
  { kind: "columns", props: [P("count", { type: "integer", min: 2, max: 6 })] },
  { kind: "form", props: [P("slug", { widget: "datalist:forms" })] },
  { kind: "list", props: [P("icon", { enum: ["alert-circle", "check"] })] },
  { kind: "heading", props: [P("level", { type: "integer", min: 1, max: 6 }), P("text")] },
];
const setup = (node, { sources = [] } = {}) => {
  const commits = [];
  const api = { kinds, getDoc: () => ({ nodes: [node] }), bindSources: sources, announce() {}, options: { forms: ["contact", "quote"] } };
  const c = createContent({ api, root: new El("div"), commit: (ops, opts) => (commits.push({ ops, opts }), true), draw() {}, kindInfo: (t) => kinds.find((k) => k.kind === t), gen: () => 0, pushRich() {}, setFields() {} });
  return { c, commits };
};
const find = (el, pred) => { let hit = null; el.walk((n) => !hit && pred(n) && (hit = n)); return hit; };

test("orderProps: Button text before link, Image address first then alt, caption, link, lightbox", () => {
  assert.deepEqual(orderProps({ t: "button" }, kinds[0].props).map((p) => p.key), ["text", "href", "variant"]);
  assert.deepEqual(orderProps({ t: "image" }, kinds[1].props).map((p) => p.key), ["src", "alt", "caption", "href", "lightbox"]);
  assert.deepEqual(orderProps({ t: "heading" }, kinds[5].props).map((p) => p.key), ["text", "level"]);
});

test("the heading level is H1-H6 buttons; the current one is pressed and a click writes level once", () => {
  const { c, commits } = setup({ id: "h", t: "heading", p: { level: 3 } });
  const field = c.propField({ id: "h", t: "heading", p: { level: 3 } }, kinds[5].props[0]);
  const btns = [];
  field.walk((n) => n.tag === "button" && btns.push(n));
  assert.deepEqual(btns.map((b) => b.textContent), ["H1", "H2", "H3", "H4", "H5", "H6"]);
  assert.deepEqual(btns.map((b) => b.attrs["aria-pressed"]), ["false", "false", "true", "false", "false", "false"]);
  btns[0].fire("click");
  assert.deepEqual(commits.map((x) => x.ops), [[{ op: "setProp", id: "h", key: "level", value: 1 }]]);
  assert.equal(btns[0].attrs["aria-pressed"], "true");
  assert.equal(btns[2].attrs["aria-pressed"], "false");
});

test("Use data: collapsed until the toggle opens it; a bound field shows the chip and Unbind instead", () => {
  const sources = [{ source: "page.title", group: "Page", label: "Title", classes: ["text"] }];
  const node = { id: "h", t: "heading", p: { text: "Hi" } };
  const { c } = setup(node, { sources });
  const field = c.propField(node, { ...kinds[5].props[1], bind: "text" });
  const toggle = find(field, (n) => n.tag === "button" && n.textContent === "Use data");
  assert.ok(toggle, "a Use data toggle");
  assert.match(toggle.attrs.class, /hm-btn--ghost hm-bs-bindtoggle/, "a quiet ghost button, not a second full button under every field");
  assert.equal(toggle.attrs["aria-expanded"], "false");
  assert.equal(find(field, (n) => n.tag === "select"), null, "no picker before opening");
  toggle.fire("click");
  assert.equal(toggle.attrs["aria-expanded"], "true");
  assert.ok(find(field, (n) => n.tag === "select"));
  toggle.fire("click");
  assert.equal(toggle.attrs["aria-expanded"], "false");

  const bound = { id: "h", t: "heading", p: {}, b: { text: "page.title" } };
  const b = setup(bound, { sources });
  const f2 = b.c.propField(bound, { ...kinds[5].props[1], bind: "text" });
  assert.ok(find(f2, (n) => n.tag === "button" && n.textContent === "Unbind"));
  assert.equal(find(f2, (n) => n.textContent === "Use data"), null);
});

const text = (el) => { const out = []; el.walk((n) => n.textContent && out.push(n.textContent)); return out; };

test("Number of columns: one commit adds the containers; a non-empty trailing column is refused with a message", async () => {
  const node = { id: "c", t: "columns", p: { count: 2 }, c: [{ id: "a", t: "container" }, { id: "b", t: "container" }] };
  const { c, commits } = setup(node);
  const field = c.propField(node, kinds[2].props[0]);
  assert.ok(text(field).includes("Number of columns"));
  const input = find(field, (n) => n.tag === "input");
  input.value = "3";
  input.fire("change");
  for (let i = 0; i < 100 && !commits.length; i++) await new Promise((r) => setTimeout(r, 20)); // the columnsops chunk is a dynamic import
  assert.equal(commits.length, 1);
  assert.deepEqual(commits[0].ops.map((o) => o.op), ["setProp", "insert"]);

  const full = { id: "c", t: "columns", p: { count: 3 }, c: [{ id: "a", t: "container" }, { id: "b", t: "container" }, { id: "d", t: "container", c: [{ id: "t", t: "text" }] }] };
  const r = setup(full);
  const f2 = r.c.propField(full, kinds[2].props[0]);
  const i2 = find(f2, (n) => n.tag === "input");
  i2.value = "2";
  i2.fire("change");
  for (let i = 0; i < 100 && !text(f2).some((t) => /Move or delete what is inside/.test(t)); i++) await new Promise((res) => setTimeout(res, 20));
  assert.equal(r.commits.length, 0);
  assert.ok(text(f2).some((t) => /Move or delete what is inside/.test(t)));
});

test("Form is a select of the site's forms; the list icon shows plain names", () => {
  const node = { id: "f", t: "form", p: { slug: "quote" } };
  const { c } = setup(node);
  const sel = find(c.propField(node, kinds[3].props[0]), (n) => n.tag === "select");
  assert.deepEqual(sel.kids.map((o) => o.textContent), ["Choose a form", "contact", "quote"]);
  const list = { id: "l", t: "list", p: {} };
  const icon = find(setup(list).c.propField(list, kinds[4].props[0]), (n) => n.tag === "select");
  assert.deepEqual(icon.kids.map((o) => o.textContent), ["Not set", "Alert circle", "Check"]);
});

test("Tabs has its Items panel, so the Content tab does not also say it has no content settings", () => {
  kinds.push({ kind: "tabs", props: [] }, { kind: "spacer", props: [] });
  const text = (node) => { const { c } = setup(node); const out = []; c.contentPanel(node).walk((n) => out.push(n.textContent)); return out.join("|"); };
  assert.doesNotMatch(text({ id: "t", t: "tabs" }), /no content settings/);
  assert.match(text({ id: "s", t: "spacer" }), /no content settings/);
});

test("a lone yes/no prop is the same switch as Show on, and a prop that cannot take data has no Use data toggle", () => {
  const node = { id: "i", t: "image", p: { lightbox: true } };
  const { c } = setup(node, { sources: [{ source: "page.title", group: "Page", label: "Title", classes: ["text"] }] });
  const field = c.propField(node, kinds[1].props[1]);
  const box = find(field, (n) => n.tag === "input");
  assert.equal(box.attrs.type, "checkbox");
  assert.equal(box.attrs.role, "switch");
  assert.match(find(field, (n) => n.tag === "label").attrs.class, /hm-switch/);
  assert.equal(find(field, (n) => n.textContent === "Use data"), null, "only a bindable prop gets the toggle");
});
