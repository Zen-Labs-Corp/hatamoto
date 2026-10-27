// The command palette: the command list, sections and ranking, and the combobox ARIA (a tiny element stub).
// Run: node --test scripts/admin-qa/unit/builder-commandk.test.mjs
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
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this.dataset = {}; this.textContent = ""; this.open = false; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { this.kids.push(...k); }
  replaceChildren(...k) { this.kids = k; }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  fire(t, e = {}) { (this.on[t] ?? []).forEach((f) => f({ preventDefault() { this.prevented = true; }, ...e })); }
  focus() {}
  showModal() { this.open = true; }
  close() { this.open = false; }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
  querySelectorAll(sel) { return sel === "[role=option]" ? this.walk().filter((n) => n.attrs?.role === "option") : []; }
}
globalThis.document = { createElement: (t) => new El(t), body: new El("body") };

const C = await import(new URL("builder/commandk.js", root));

const items = [
  { key: "heading", kind: "heading", name: "Heading", hint: "A headline", group: "Elements" },
  { key: "container", kind: "container", name: "Container", hint: "", group: "Layout" },
  { key: "component:hero", kind: "component", type: "hero", name: "Hero", hint: "Theme section", group: "Patterns" },
  { key: "blk:b1", kind: "block", name: "Promo", hint: "", group: "Blocks" },
];
const names = (cmds) => cmds.map((c) => c.name);

test("commandsFor lists exactly the palette commands; Site styles is for owners, Preview needs an address", () => {
  const editor = names(C.commandsFor({}));
  assert.deepEqual(editor, ["Undo", "Redo", "Desktop", "Tablet", "Phone", "Show or hide Navigator", "Show or hide Inspector", "History", "Keyboard shortcuts", "Save as block…", "Save as template…"]);
  assert.deepEqual(names(C.commandsFor({ canStyle: true, preview: true })).slice(-2), ["Site styles", "Preview"]);
  assert.ok(!editor.includes("Site styles"));
});

test("sections: Recent first (no query), then Patterns, Elements, Blocks, Commands; a query ranks and drops empty ones", () => {
  const commands = C.commandsFor({});
  const all = C.sectionsFor({ items, commands, recents: ["blk:b1", "gone"] });
  assert.deepEqual(all.map((s) => s.name), ["Recent", "Patterns", "Elements", "Blocks", "Commands"]);
  assert.deepEqual(all[0].rows.map((r) => r.name), ["Promo"], "unknown recent keys are dropped");
  const q = C.sectionsFor({ items, commands, recents: ["heading"], query: "hea" });
  assert.deepEqual(q.map((s) => s.name), ["Elements"], "no Recent while searching");
  const cmd = C.sectionsFor({ items, commands, query: "his" });
  assert.deepEqual(cmd.map((s) => [s.name, s.rows.map((r) => r.name)]), [["Commands", ["History"]]]);
});

test("combobox: ArrowDown/Up move aria-activedescendant and aria-selected, wrapping", () => {
  const run = [];
  const ck = C.createCommandK({ getItems: () => items, onInsert: () => {}, onRun: (id) => run.push(id), storage: { getItem: () => null, setItem() {} } });
  ck.open();
  assert.equal(ck.el.open, true);
  const input = ck.el.walk().find((n) => n.attrs.role === "combobox");
  const opts = () => ck.el.walk().filter((n) => n.attrs.role === "option");
  assert.equal(input.attrs["aria-controls"], "hm-bs-ck-list");
  assert.equal(input.attrs["aria-activedescendant"], "hm-bs-ck-0");
  assert.equal(opts()[0].attrs["aria-selected"], "true");
  input.fire("keydown", { key: "ArrowDown" });
  assert.equal(input.attrs["aria-activedescendant"], "hm-bs-ck-1");
  assert.deepEqual(opts().map((o) => o.attrs["aria-selected"]).filter((v) => v === "true").length, 1);
  input.fire("keydown", { key: "ArrowUp" });
  input.fire("keydown", { key: "ArrowUp" });
  assert.equal(input.attrs["aria-activedescendant"], `hm-bs-ck-${opts().length - 1}`, "wraps to the last");
  input.fire("keydown", { key: "Enter" });
  assert.equal(run.length, 1, "the last row is a command");
  assert.equal(ck.el.open, false, "Enter closes");
});

test("Enter inserts an element and closes; Shift+Enter inserts and stays open; both are remembered", () => {
  const inserted = [];
  const store = {};
  const ck = C.createCommandK({ getItems: () => items, user: "u1", storage: { getItem: (k) => store[k] ?? null, setItem: (k, v) => (store[k] = v) }, onInsert: (i, o) => inserted.push([i.key, o.keepOpen]), onRun: () => {} });
  ck.open();
  const input = ck.el.walk().find((n) => n.attrs.role === "combobox");
  input.value = "heading";
  input.fire("input");
  input.fire("keydown", { key: "Enter", shiftKey: true });
  assert.deepEqual(inserted, [["heading", true]]);
  assert.equal(ck.el.open, true);
  input.fire("keydown", { key: "Enter" });
  assert.deepEqual(inserted[1], ["heading", false]);
  assert.equal(ck.el.open, false);
  assert.deepEqual(JSON.parse(store["hm-builder-recent:u1"]), ["heading"]);
});

test("a query lists the section of the best match first, and commands only when their name matches", () => {
  const withHero = [...items, { key: "component:herald", kind: "component", type: "herald", name: "Herald section", hint: "Theme section", group: "Patterns" }];
  const commands = C.commandsFor({});
  const he = C.sectionsFor({ items: withHero, commands, query: "he" });
  assert.equal(he[0].name, "Elements");
  assert.equal(he[0].rows[0].name, "Heading", "Enter would insert Heading, not a pattern");
  const co = C.sectionsFor({ items, commands, query: "co" });
  assert.ok(!co.some((s) => s.name === "Commands"), "'command' is not a name: " + co.map((s) => s.name));
  assert.deepEqual(C.sectionsFor({ items, commands, query: "undo" })[0].rows.map((r) => r.name), ["Undo"]);
});

test("Home and End stay with the caret in the search; Mac shows ⌘ ⇧ ⌥ in the shortcut hints", () => {
  const ck = C.createCommandK({ getItems: () => items, onInsert: () => {}, onRun: () => {}, storage: { getItem: () => null, setItem() {} } });
  ck.open();
  const input = ck.el.walk().find((n) => n.attrs.role === "combobox");
  input.fire("keydown", { key: "ArrowDown" });
  const moved = input.attrs["aria-activedescendant"];
  for (const key of ["Home", "End"]) {
    const e = { key, prevented: false, preventDefault() { this.prevented = true; } };
    (input.on.keydown ?? []).forEach((f) => f(e));
    assert.equal(e.prevented, false, key + " is the caret's");
    assert.equal(input.attrs["aria-activedescendant"], moved, key + " does not move the active row");
  }
  assert.equal(C.commandsFor({ mac: true }).find((c) => c.id === "redo").shortcut, "⌘ ⇧ Z");
  assert.equal(C.commandsFor({ mac: true }).find((c) => c.id === "togglenav").shortcut, "⌘ ⌥ [");
  assert.equal(C.commandsFor({ mac: false }).find((c) => c.id === "redo").shortcut, "Ctrl Shift Z");
  assert.equal(C.commandsFor({ mac: true }).find((c) => c.id === "mkblock").shortcut, undefined);
});
