// Scrubbable field labels: dragging a row's label changes its value (one undo step per drag); the field stays the keyboard path.
// Run: node --test scripts/admin-qa/unit/builder-labelscrub.test.mjs
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
    Object.assign(this, { tag, attrs: {}, kids: [], on: {}, hidden: false, dataset: {}, className: "", textContent: "", value: "", classes: new Set(), calls: [] });
    this.classList = { toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c)), add: (c) => this.classes.add(c) };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "class") this.className = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { for (const x of k) { this.kids.push(x); x.parent = this; } }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  setPointerCapture() {}
  focus() { this.calls.push("focus"); }
  select() { this.calls.push("select"); }
  fire(t, ev = {}) { (this.on[t] ?? []).forEach((f) => f({ pointerId: 1, clientX: 0, clientY: 0, preventDefault() {}, ...ev })); }
  walk(f) { f(this); this.kids.forEach((k) => k.walk(f)); }
}
globalThis.document = { createElement: (t) => new El(t) };

const { row } = await import(new URL("builder/controlsui.js", root));

const make = (scrub) => {
  const label = new El("label");
  label.setAttribute("for", "f1");
  label.textContent = "Font size";
  const applied = [];
  const view = row({ id: "f1", name: "Font size", label, body: [], status: null, note: null, onReset() {}, scrub: scrub === false ? undefined : { get: () => 16, apply: (n, opts) => applied.push([n, opts.coalesce]), step: 1, bounds: { min: 8, max: 96 }, short: "Sz", ...scrub } });
  return { label, applied, view };
};
const drag = (el, xs, extra = {}) => {
  el.fire("pointerdown", { clientX: 100 });
  for (const x of xs) el.fire("pointermove", { clientX: x, ...extra });
  el.fire("pointerup");
};

test("dragging the label sideways commits repeatedly with one coalesce key", () => {
  const { label, applied } = make({});
  drag(label, [105, 110, 120]);
  assert.equal(applied.at(-1)[0], 36, "start 16 + 20 px");
  assert.ok(applied.length >= 3);
  assert.equal(new Set(applied.map((a) => a[1])).size, 1);
  assert.match(applied[0][1], /^ls:f1:/);
});

test("Shift scrubs by ten; the value stays inside the bounds", () => {
  const { label, applied } = make({});
  drag(label, [104], { shiftKey: true });
  assert.equal(applied.at(-1)[0], 56, "16 + 4 px x 10");
  drag(label, [100 - 500]);
  assert.equal(applied.at(-1)[0], 8, "clamped to the minimum");
});

test("a second drag is a second session; pointercancel ends the first", () => {
  const { label, applied } = make({});
  label.fire("pointerdown", { clientX: 100 });
  label.fire("pointermove", { clientX: 110 });
  label.fire("pointercancel");
  label.fire("pointermove", { clientX: 130 });
  const before = applied.length;
  assert.equal(before, 1, "moves after the cancel write nothing");
  drag(label, [110]);
  assert.notEqual(applied[0][1], applied.at(-1)[1]);
});

test("the label stays a real label: same for=, the full name is the accessible name, the short form is drawn", () => {
  const { label } = make({});
  assert.equal(label.attrs.for, "f1");
  assert.ok(label.classes.has("hm-bs-scrub"));
  const spans = label.kids.map((k) => [k.attrs["aria-hidden"] ?? null, k.textContent]);
  assert.deepEqual(spans, [["true", "Sz"], [null, "Font size"]]);
  assert.equal(label.attrs.title, "Drag to change");
});

test("a press without movement is a click and writes nothing", () => {
  const { label, applied } = make({});
  drag(label, [101]);
  assert.equal(applied.length, 0);
});

test("no scrub attached when scrub is absent", () => {
  const { label, view } = make(false);
  assert.equal(Object.keys(label.on).length, 0);
  assert.equal(label.textContent, "Font size");
  assert.ok(!label.classes.has("hm-bs-scrub"));
  assert.ok(view.el);
});
