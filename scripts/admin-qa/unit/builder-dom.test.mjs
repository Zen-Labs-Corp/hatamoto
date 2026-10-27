// dom.js actButton / warnNote: clone the server-rendered hm/button and hm/callout templates, fill the
// label slot, add attributes and handlers; without the template a plain element still works.
// Run: node --test scripts/admin-qa/unit/builder-dom.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

class El {
  constructor(tag, slots = {}) { this.tag = tag; this.attrs = {}; this.on = {}; this.textContent = ""; this.slots = slots; this.kids = []; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  addEventListener(t, f) { this.on[t] = f; }
  append(...k) { this.kids.push(...k); }
  querySelector(sel) { return this.slots[sel] ?? null; }
  cloneNode() {
    const c = new El(this.tag, Object.fromEntries(Object.entries(this.slots).map(([k, v]) => [k, new El(v.tag)])));
    c.attrs = { ...this.attrs };
    return c;
  }
}
const tpls = {};
globalThis.document = {
  createElement: (t) => new El(t),
  querySelector: (sel) => { const m = /data-hm-bs-(\w+)/.exec(sel); return tpls[m[1]] ? { content: { firstElementChild: tpls[m[1]] } } : null; },
};
const { actButton, warnNote } = await import("../../../backend/web/admin/js/builder/dom.js");

test("without templates a plain button and paragraph are built", () => {
  const b = actButton("Save", { "data-focus": "x" });
  assert.equal([b.tag, b.textContent, b.attrs["data-focus"]].join("|"), "button|Save|x");
  assert.equal(warnNote("Careful").tag, "p");
});

test("with templates the label goes in the slot, attributes and handlers are added, the template is untouched", () => {
  tpls.act = new El("button", { span: new El("span") });
  tpls.warn = new El("div", { ".hm-callout__body": new El("div") });
  const click = () => {};
  const b = actButton("Reset", { "aria-label": "Reset x", onclick: click, hidden: true, skip: false });
  assert.equal(b.slots.span.textContent, "Reset");
  assert.equal(b.attrs["aria-label"], "Reset x");
  assert.equal(b.attrs.hidden, "");
  assert.equal(b.on.click, click);
  assert.ok(!("skip" in b.attrs));
  assert.equal(tpls.act.slots.span.textContent, "", "the template itself is not modified");
  const w = warnNote("Add alt text", { role: "status" });
  assert.equal(w.slots[".hm-callout__body"].textContent, "Add alt text");
  assert.equal(w.attrs.role, "status");
});

test("a class prop adds to the classes the template carries", () => {
  tpls.act.attrs.class = "hm-btn hm-btn--sm";
  assert.equal(actButton("", { class: "hm-bs-reset" }).attrs.class, "hm-btn hm-btn--sm hm-bs-reset");
  tpls.act.attrs = {};
});
