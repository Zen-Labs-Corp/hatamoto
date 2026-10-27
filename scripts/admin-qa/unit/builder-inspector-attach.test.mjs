// Inspector shell: attach(el) chrome outlives draw(), and the Content panel renders once its lazy chunk resolves.
// A small element stub stands in for the DOM.
// Run: node --test scripts/admin-qa/unit/builder-inspector-attach.test.mjs
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
  addEventListener() {}
  querySelector() { return null; }
  closest() { return null; }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t), activeElement: null };
globalThis.window = globalThis;

const { mountInspector } = await import(new URL("builder/inspector.js", root));

const doc = { nodes: [{ id: "a", t: "text", p: { text: "Hi" } }] };
const mount = () => {
  const el = new El("div");
  el.top = true;
  const ins = mountInspector(el, {
    getDoc: () => doc,
    getSelected: () => "a",
    kinds: [{ kind: "text", props: [{ key: "text", type: "string" }] }],
    sections: [],
    device: () => "base",
    styleTable: () => [],
    commit: () => true,
    force() {},
    design: { presets: [] },
    announce() {},
  });
  return { el, ins };
};
const find = (el, pred) => { let hit = null; el.walk((n) => !hit && pred(n) && (hit = n)); return hit; };
const settle = () => new Promise((r) => setTimeout(r, 50));

test("attach(el) stays connected across draw() and its remover detaches it", () => {
  const { el, ins } = mount();
  const extra = new El("div");
  const off = ins.attach(extra);
  ins.draw();
  assert.ok(extra.isConnected, "connected after the first draw");
  ins.draw();
  assert.ok(extra.isConnected, "connected after a redraw");
  assert.ok(find(el, (n) => n.attrs["data-hm-inspector-slot"] !== undefined), "the slot is in the panel");
  off();
  assert.ok(!extra.isConnected);
});

test("the Content panel renders its fields once the chunk resolves", async () => {
  const { el, ins } = mount();
  ins.draw();
  const drawn = () => find(el, (n) => n.attrs["data-prop"] === "text" || n.dataset.prop === "text");
  for (let i = 0; i < 100 && !drawn(); i++) await settle(); // the lazy chunk can take longer than one tick on a busy machine
  assert.ok(drawn(), "the Text field is drawn");
});
