// The class panel puts focus on a visible control after a class action (the editor select is hidden then).
// Run: node --test scripts/admin-qa/unit/builder-presetpanel-focus.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, dataset: {}, hidden: false, textContent: "", value: "", parent: null });
    this.classList = { toggle() {}, add() {} };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "hidden") this.hidden = true; }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { this.kids.push(x); x.parent = this; } }
  replaceChildren(...k) { this.kids = []; this.append(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  closest(sel) { for (let n = this; n; n = n.parent) if (sel === "[hidden]" && n.hidden) return n; return null; }
}
globalThis.document = { createElement: (t) => new El(t), querySelector: () => null };
const { presetPanel } = await import(new URL("builder/presetpanel.js", root));

test("after a class action focus lands on a visible control, not the hidden editor select", async () => {
  const focused = [];
  const mk = (name, hidden = false) => Object.assign(new El("button"), { hidden, focus: () => focused.push(name) });
  const more = mk("more");
  const tab = mk("tab");
  let editor = null; // the panel's own (hidden) editor, found by the data-focus query
  const page = { querySelector: (q) => (q === '[data-focus="select"]' ? editor : q === ".hm-bs-more" ? more : q === '[role="tab"][tabindex="0"]' ? tab : null) };
  const node = { id: "n", t: "text", p: {}, s: {} };
  const api = { design: { presets: [{ name: "card", label: "Card", s: {} }], canCreate: false, tokens: null }, styleTable: () => [] };
  const panel = presetPanel(node, () => node, { api, commit: () => true, draw() {}, root: page, id: "p", ready: () => Promise.resolve() });
  const select = panel.el.kids[0].kids[0].kids[1]; // the class select inside the hidden .hm-bs-class
  editor = Object.assign(select, { focus: () => focused.push("select") });
  assert.ok(editor.closest("[hidden]"), "the editor is hidden until the menu reveals it");
  select.value = "card";
  select.on.change[0]();
  await new Promise((r) => setTimeout(r));
  assert.deepEqual(focused, ["more"]);
});
