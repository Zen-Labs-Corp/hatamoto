// Ctrl/Cmd+K opens the command palette from anywhere except a text edit on the canvas.
// Run: node --test scripts/admin-qa/unit/builder-keymap.test.mjs
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
const { resolveKey } = await import(new URL("builder/keymap.js", root));

const k = (key, extra = {}) => ({ key, ctrlKey: true, ...extra });

test("Ctrl/Cmd+K resolves commandk in the tree, on the page, in a text input and with nothing selected", () => {
  for (const ctx of [{ tree: true, free: true, selected: true }, { free: true, selected: false }, { text: true, free: false, selected: true }, {}]) {
    assert.equal(resolveKey(k("k"), ctx), "commandk", JSON.stringify(ctx));
    assert.equal(resolveKey(k("K"), ctx), "commandk");
  }
  assert.equal(resolveKey({ key: "k", metaKey: true }, {}), "commandk");
});

test("not while the canvas has a text edit open (that is insert link), and not with Shift or Alt", () => {
  assert.equal(resolveKey(k("k"), { free: true, canvasText: true }), null);
  assert.equal(resolveKey(k("k", { shiftKey: true }), { free: true }), null);
  assert.equal(resolveKey(k("k", { altKey: true }), { free: true }), null);
});

test("a plain k is not the palette, and the other keys are unchanged", () => {
  assert.equal(resolveKey({ key: "k" }, { free: true }), null);
  assert.equal(resolveKey(k("s"), { text: true }), "save");
  assert.equal(resolveKey({ key: "/" }, { free: true }), "palette");
  assert.equal(resolveKey(k("z"), { free: true }), "undo");
});

test("Enter on the page or canvas pane with a selection enters the element; on a control, in the tree or in a field it does not", () => {
  const enter = { key: "Enter" };
  assert.equal(resolveKey(enter, { free: true, selected: true }), "enter");
  assert.equal(resolveKey(enter, { free: true, selected: false }), null);
  assert.equal(resolveKey(enter, { free: true, selected: true, control: true }), null, "a toolbar button keeps its Enter");
  assert.equal(resolveKey(enter, { free: true, selected: true, text: true }), null);
  assert.equal(resolveKey(enter, { free: true, tree: true, selected: true }), "inspect", "the tree keeps Enter = inspect");
  assert.equal(resolveKey({ key: "Enter", shiftKey: true }, { free: true, selected: true }), null);
});

const key = (k, extra = {}) => ({ key: k, ...extra });
const page = { free: true, selected: true };

test("Esc selects the parent: needs a selection, not in text, not on a control, no Shift", () => {
  assert.equal(resolveKey(key("Escape"), page), "parent");
  assert.equal(resolveKey(key("Escape"), { free: true, tree: true, selected: true }), "parent");
  assert.equal(resolveKey(key("Escape"), { free: true }), null);
  assert.equal(resolveKey(key("Escape"), { ...page, text: true }), null);
  assert.equal(resolveKey(key("Escape"), { ...page, control: true }), null);
  assert.equal(resolveKey(key("Escape"), { selected: true }), null, "outside the free zones");
  assert.equal(resolveKey(key("Escape", { shiftKey: true }), page), null);
  assert.equal(resolveKey(key("Escape", { ctrlKey: true }), page), null);
});

test("Alt+Up/Down move on the page and in the tree, Alt+Left/Right only in the tree", () => {
  for (const ctx of [page, { ...page, tree: true }]) {
    assert.equal(resolveKey(key("ArrowUp", { altKey: true }), ctx), "moveup");
    assert.equal(resolveKey(key("ArrowDown", { altKey: true }), ctx), "movedown");
  }
  assert.equal(resolveKey(key("ArrowLeft", { altKey: true }), page), null);
  assert.equal(resolveKey(key("ArrowRight", { altKey: true }), page), null);
  assert.equal(resolveKey(key("ArrowLeft", { altKey: true }), { ...page, tree: true }), "outdent");
  assert.equal(resolveKey(key("ArrowRight", { altKey: true }), { ...page, tree: true }), "indent");
  assert.equal(resolveKey(key("ArrowUp", { altKey: true }), { free: true }), null);
  assert.equal(resolveKey(key("ArrowUp", { altKey: true }), { ...page, text: true }), null);
});

test("Backspace removes the selection on the page too; not in text, not on a control, not unselected", () => {
  assert.equal(resolveKey(key("Backspace"), page), "remove");
  assert.equal(resolveKey(key("Backspace"), { free: true, tree: true, selected: true }), "remove");
  assert.equal(resolveKey(key("Backspace"), { ...page, text: true }), null);
  assert.equal(resolveKey(key("Backspace"), { ...page, control: true }), null);
  assert.equal(resolveKey(key("Backspace"), { free: true }), null);
});

test("Up/Down on the page step sections; the tree keeps its row map; nothing selected is the page's scroll", () => {
  assert.equal(resolveKey(key("ArrowUp"), page), "prevsection");
  assert.equal(resolveKey(key("ArrowDown"), page), "nextsection");
  assert.equal(resolveKey(key("ArrowUp"), { ...page, tree: true }), "prev");
  assert.equal(resolveKey(key("ArrowDown"), { ...page, tree: true }), "next");
  assert.equal(resolveKey(key("ArrowDown"), { free: true }), null);
  assert.equal(resolveKey(key("ArrowDown"), { ...page, control: true }), null);
  assert.equal(resolveKey(key("ArrowDown"), { ...page, text: true }), null);
  assert.equal(resolveKey(key("ArrowDown", { ctrlKey: true }), page), null);
});

test("1/2/3 switch the device from the page and the tree, never from text, a control or with a modifier", () => {
  for (const ctx of [{ free: true }, page, { free: true, tree: true }]) {
    assert.deepEqual(["1", "2", "3"].map((d) => resolveKey(key(d), ctx)), ["device-base", "device-md", "device-sm"]);
  }
  assert.equal(resolveKey(key("4"), page), null);
  assert.equal(resolveKey(key("1"), { ...page, text: true }), null);
  assert.equal(resolveKey(key("1"), { ...page, control: true }), null);
  assert.equal(resolveKey(key("1"), { selected: true }), null, "outside the free zones");
  for (const m of [{ ctrlKey: true }, { metaKey: true }, { altKey: true }, { shiftKey: true }]) assert.equal(resolveKey(key("2", m), page), null, JSON.stringify(m));
});
