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

test("Enter on the page or canvas pane with a selection starts a text edit; on a control, in the tree or in a field it does not", () => {
  const enter = { key: "Enter" };
  assert.equal(resolveKey(enter, { free: true, selected: true }), "edittext");
  assert.equal(resolveKey(enter, { free: true, selected: false }), null);
  assert.equal(resolveKey(enter, { free: true, selected: true, control: true }), null, "a toolbar button keeps its Enter");
  assert.equal(resolveKey(enter, { free: true, selected: true, text: true }), null);
  assert.equal(resolveKey(enter, { free: true, tree: true, selected: true }), "inspect", "the tree keeps Enter = inspect");
  assert.equal(resolveKey({ key: "Enter", shiftKey: true }, { free: true, selected: true }), null);
});
