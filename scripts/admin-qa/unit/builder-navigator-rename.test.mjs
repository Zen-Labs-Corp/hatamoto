// A rename input that cannot take focus (hidden pane) must not block the tree from painting.
// Run: node --test scripts/admin-qa/unit/builder-navigator-rename.test.mjs
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

let focusable = false;
function el(tag) {
  const e = {
    tag, attrs: {}, kids: [], isConnected: true, style: { setProperty() {} }, value: "",
    setAttribute(k, v) { this.attrs[k] = v; if (k === "value") this.value = v; },
    append(...k) { this.kids.push(...k); },
    prepend(...k) { this.kids.unshift(...k); },
    replaceChildren(...k) { this.kids = k; },
    set textContent(v) { this.kids = [String(v)]; },
    addEventListener() {},
    focus() { if (focusable) globalThis.document.activeElement = this; },
    select() {},
    contains: () => false,
  };
  return e;
}
globalThis.document = { createElement: el, activeElement: null };
globalThis.requestAnimationFrame = (f) => (f(), 0);
const { mountNavigator } = await import(new URL("builder/navigator.js", root));

const doc = { v: 2, nodes: [{ id: "a", t: "container", c: [] }] };
const mount = () => {
  const painted = [];
  const rootEl = { closest: () => ({ querySelector: () => null }), contains: () => false, replaceChildren: (...k) => painted.push(k), addEventListener() {}, querySelector: () => null, querySelectorAll: () => [] };
  const warn = console.warn;
  console.warn = () => {};
  const tree = mountNavigator(rootEl, { getDoc: () => doc, getSelected: () => "a", onSelect() {} });
  console.warn = warn;
  return { tree, painted };
};

test("a rename input that never gets focus is dropped, so paint works again", async () => {
  focusable = false;
  const { tree, painted } = mount();
  tree.rename("a");
  const n = painted.length;
  await new Promise((r) => setTimeout(r, 5));
  assert.ok(painted.length > n, "the tree repainted without the input");
  tree.draw();
  const m = painted.length;
  tree.draw();
  assert.ok(painted.length > m, "later paints are not blocked");
});

test("a focused rename input stays", async () => {
  focusable = true;
  const { tree, painted } = mount();
  tree.rename("a");
  const n = painted.length;
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(painted.length, n);
  tree.draw();
  assert.equal(painted.length, n, "paint leaves a live input alone");
});
