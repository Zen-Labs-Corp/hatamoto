// The Style tab's Show on switches and the "Show as" select stay in step, and a switch is one commit
// that leaves the other widths as they were. A small element stub stands in for the DOM.
// Run: node --test scripts/admin-qa/unit/builder-visibility.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, hidden: false, dataset: {}, className: "", textContent: "", value: "", checked: false, classes: new Set() });
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)), add: (c) => this.classes.add(c) };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "hidden") this.hidden = true; }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { this.kids.push(x); x.parent = this; } }
  remove() { if (this.parent) this.parent.kids = this.parent.kids.filter((x) => x !== this); }
  replaceWith(n) { const p = this.parent; p.kids[p.kids.indexOf(this)] = n; n.parent = p; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t) { (this.on[t] ?? []).forEach((f) => f({})); }
  querySelector() { return null; }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { querySelector: () => null, createElement: (t) => new El(t) };
globalThis.CSS = { escape: (s) => s };

const { mountStyle } = await import(new URL("builder/style.js", root));

const table = [
  { key: "w", kind: "text", props: ["width"], tokens: {}, pattern: ".*" }, // Size: the Layout group, which holds Show on, needs a key
  { key: "hide", kind: "enum", props: ["display"], enum: { 1: "none" } },
  { key: "display", kind: "enum", props: ["display"], enum: { block: "block", flex: "flex", none: "none" } },
];
const mount = (node, extra = {}) => {
  const calls = [];
  const el = mountStyle({
    node, table, bp: "sm", display: "block",
    commit: (key, value, bp) => (calls.push([{ key, value }, bp]), true),
    commitMany: (writes, bp) => (calls.push([writes, bp]), true),
    ...extra,
  });
  const all = (pred) => { const r = []; el.walk((n) => pred(n) && r.push(n)); return r; };
  return { el, calls, boxes: () => all((n) => n.attrs.type === "checkbox" && n.attrs.role === "switch"), select: () => all((n) => n.tag === "select" && n.attrs["aria-label"] !== "State")[0] };
};

test("switching Phone on under a Desktop hide is one commit, only Phone changes, and Show as follows it", () => {
  const m = mount({ t: "text", s: { base: { hide: "1" } } });
  const [desktop, tablet, phone] = m.boxes();
  assert.deepEqual([desktop.checked, tablet.checked, phone.checked], [false, false, false]);
  phone.checked = true;
  phone.fire("change");
  assert.deepEqual(m.calls, [[[{ bp: "sm", key: "display", value: "block" }], "sm"]], "one batch, written at Phone");
  assert.deepEqual([desktop.checked, tablet.checked, phone.checked], [false, false, true]);
  assert.equal(m.select().value, "block", "Show as shows the written display");
});

test("switching Desktop off keeps Tablet and Phone shown, in one batch", () => {
  const m = mount({ t: "text" });
  const [desktop, tablet, phone] = m.boxes();
  desktop.checked = false;
  desktop.fire("change");
  assert.deepEqual(m.calls, [[[{ bp: "base", key: "hide", value: "1" }, { bp: "md", key: "display", value: "block" }], "sm"]]);
  assert.deepEqual([desktop.checked, tablet.checked, phone.checked], [false, true, true]);
});

test("the width being edited is ringed and the switches are switches", () => {
  const m = mount({ t: "text" });
  assert.deepEqual(m.boxes().map((b) => b.attrs["aria-current"] ?? null), [null, null, "true"]);
  assert.equal(m.boxes().length, 3);
});

test("Show as none switches the width off", () => {
  const m = mount({ t: "text" });
  const sel = m.select();
  sel.value = "none";
  sel.fire("change");
  assert.equal(m.boxes()[2].checked, false);
});
