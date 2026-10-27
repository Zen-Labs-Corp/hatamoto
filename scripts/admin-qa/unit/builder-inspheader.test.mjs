// Inspector header: name, device chip and the "..." menu (Paste disabled with a title until a style is copied).
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


const { header, deviceChipText } = await import(new URL("builder/inspheader.js", root));
const { clipboard } = await import(new URL("builder/design.js", root));

const api = (device, extra = {}) => ({ device: () => device, copyStyle() {}, pasteStyle() {}, ...extra });
const all = (el) => { const out = []; el.walk((n) => out.push(n)); return out; };
const text = (n) => n.textContent;

test("deviceChipText: nothing on Desktop, Editing Tablet / Phone otherwise", () => {
  assert.equal(deviceChipText("base"), "");
  assert.equal(deviceChipText("md"), "Editing Tablet");
  assert.equal(deviceChipText("sm"), "Editing Phone");
});

test("the header keeps focus order name, chip, menu button, and hides the chip on Desktop", () => {
  const tablet = header({ node: { id: "a" }, api: api("md"), name: "Heading" });
  const tags = tablet.kids.map((k) => k.tag);
  assert.deepEqual(tags, ["h3", "span", "button", "div"], "name, chip, opener, menu");
  assert.equal(text(tablet.kids[1]), "Editing Tablet");
  assert.match(tablet.kids[1].attrs.class ?? "", /hm-badge--tablet/, "the chip recolours through a badge modifier, not an attribute override");
  const desktop = header({ node: { id: "a" }, api: api("base"), name: "Heading" });
  assert.deepEqual(desktop.kids.map((k) => k.tag), ["h3", "button", "div"]);
});

test("Paste style says in its own text that a style must be copied first, and stays focusable", () => {
  clipboard.clear?.();
  const el = header({ node: { id: "a" }, api: api("base"), name: "Heading" });
  const paste = all(el).find((n) => n.textContent === "Paste style (copy a style first)");
  assert.ok(paste, "the reason is the item's text");
  assert.equal(paste.attrs.disabled, undefined, "a disabled item is skipped by the menu's arrow keys");
  assert.equal(paste.attrs["aria-disabled"], undefined);
  assert.ok(all(el).some((n) => n.textContent === "Copy style"));
});

test("Restore from an earlier version appears only when the screen offers it, and passes the node id", () => {
  assert.ok(!all(header({ node: { id: "a" }, api: api("base"), name: "H" })).some((n) => /Restore/.test(n.textContent)));
  let got = null;
  const el = header({ node: { id: "a" }, api: api("base", { restore: (id) => (got = id) }), name: "H" });
  const row = all(el).find((n) => /Restore from an earlier version/.test(n.textContent));
  assert.ok(row);
  row.on.click[0]();
  assert.equal(got, "a");
});
