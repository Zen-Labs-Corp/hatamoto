// The history, picker and element-menu popovers give focus back only when it was inside them (or nowhere),
// never after a click elsewhere. Run: node --test scripts/admin-qa/unit/builder-popover-focus.test.mjs
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

// A just-enough DOM: elements with listeners, children, focus and popover show/hide.
const body = { tag: "body", kids: [], append(...k) { this.kids.push(...k); } };
function el(tag) {
  const e = {
    tag, attrs: {}, kids: [], isConnected: true, listeners: {}, style: { setProperty() {} }, dataset: {}, offsetWidth: 10, offsetHeight: 10,
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...k) { this.kids.push(...k); },
    replaceChildren(...k) { this.kids = k; },
    set textContent(v) { this.kids = [String(v)]; },
    addEventListener(t, f) { (this.listeners[t] ??= []).push(f); },
    querySelector(sel) { return this.find(sel); },
    find(sel) {
      for (const k of this.kids) {
        if (typeof k === "string") continue;
        if (sel === '[tabindex="0"]' ? k.attrs.tabindex === "0" : false) return k;
        const d = k.find?.(sel);
        if (d) return d;
      }
      return null;
    },
    contains(x) { return x === this || this.kids.some((k) => typeof k !== "string" && k.contains(x)); },
    focus() { document.activeElement = this; },
    remove() { if (document.activeElement && this.contains(document.activeElement)) document.activeElement = body; },
    scrollIntoView() {},
    getBoundingClientRect: () => ({ left: 0, bottom: 0, width: 0 }),
    showPopover() {},
    hidePopover() {
      // as a browser: focus is read at beforetoggle, then put back to the previous element before toggle
      for (const f of this.listeners.beforetoggle ?? []) f({ newState: "closed" });
      if (document.restoreTo) document.activeElement = document.restoreTo;
      for (const f of this.listeners.toggle ?? []) f({ newState: "closed" });
    },
  };
  return e;
}
globalThis.document = { createElement: el, activeElement: body, body, documentElement: { clientWidth: 1000 } };
globalThis.innerHeight = 800;
globalThis.window = globalThis;

const { openHistory } = await import(new URL("builder/history.js", root));
const { openMenu } = await import(new URL("builder/ctxmenu.js", root));
const { openPicker } = await import(new URL("builder/picker.js", root));

const store = { past: [{ label: "a", t: 0 }], future: [] };
const outside = el("input"); // a field elsewhere on the page

const cases = {
  history: (anchor) => openHistory({ anchor, getStore: () => store, onJump() {}, onClose() {} }),
  picker: (anchor) => openPicker({ anchor, onPick() {}, onChoose() {} }),
  menu: (anchor) => openMenu({ items: [{ id: "x", label: "X" }], x: 0, y: 0, onPick() {}, restore: () => anchor.focus() }),
};

for (const [name, open] of Object.entries(cases)) {
  test(`${name}: a click elsewhere keeps focus where it went; closing from inside returns it`, () => {
    const anchor = el("button");
    document.activeElement = body;
    const handle = open(anchor);
    const panel = body.kids.at(-1);
    document.activeElement = outside; // light dismiss by clicking into a field
    panel.hidePopover();
    assert.equal(document.activeElement, outside, "focus was not stolen");

    document.activeElement = body;
    open(anchor);
    const panel2 = body.kids.at(-1);
    document.activeElement = panel2.kids.find((k) => typeof k !== "string") ?? panel2; // focus inside the panel
    if (!panel2.contains(document.activeElement)) document.activeElement = panel2;
    document.restoreTo = el("li"); // the browser puts focus back on the element that had it before the popover opened
    panel2.hidePopover();
    document.restoreTo = null;
    assert.equal(document.activeElement, anchor, "focus returns after Esc or a pick");
    void handle;
  });
}
