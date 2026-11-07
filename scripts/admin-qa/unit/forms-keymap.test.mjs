// The forms designer's key table. Run: node --test scripts/admin-qa/unit/forms-keymap.test.mjs
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
const { resolveKey } = await import(new URL("forms/keymap.js", root));

const canvas = { selected: true };
const inField = { selected: true, text: true };
const mod = (key, extra = {}) => ({ key, ctrlKey: true, ...extra });

test("Alt+Up/Down move the selected field, but not from a text control or without a selection", () => {
  assert.equal(resolveKey({ key: "ArrowUp", altKey: true }, canvas), "move-up");
  assert.equal(resolveKey({ key: "ArrowDown", altKey: true }, canvas), "move-down");
  assert.equal(resolveKey({ key: "ArrowUp", altKey: true }, inField), null);
  assert.equal(resolveKey({ key: "ArrowUp", altKey: true }, {}), null);
});

test("undo and redo, with Ctrl+Y and Cmd; text controls keep their own undo", () => {
  assert.equal(resolveKey(mod("z"), canvas), "undo");
  assert.equal(resolveKey(mod("Z", { shiftKey: true }), canvas), "redo");
  assert.equal(resolveKey(mod("y"), canvas), "redo");
  assert.equal(resolveKey({ key: "z", metaKey: true }, {}), "undo");
  assert.equal(resolveKey(mod("z"), inField), null);
  assert.equal(resolveKey(mod("y"), inField), null);
});

test("Ctrl/Cmd+S saves everywhere, including text controls", () => {
  assert.equal(resolveKey(mod("s"), inField), "save");
  assert.equal(resolveKey({ key: "S", metaKey: true }, {}), "save");
});

test("duplicate, delete, deselect and step need a selection and a non-text target", () => {
  assert.equal(resolveKey(mod("d"), canvas), "duplicate");
  assert.equal(resolveKey(mod("d"), {}), null);
  assert.equal(resolveKey({ key: "Delete" }, canvas), "delete");
  assert.equal(resolveKey({ key: "Backspace" }, canvas), "delete");
  assert.equal(resolveKey({ key: "Delete" }, inField), null);
  assert.equal(resolveKey({ key: "Delete" }, {}), null);
  assert.equal(resolveKey({ key: "Escape" }, canvas), "deselect");
  assert.equal(resolveKey({ key: "ArrowUp" }, canvas), "prev");
  assert.equal(resolveKey({ key: "ArrowDown" }, canvas), "next");
  assert.equal(resolveKey({ key: "ArrowDown" }, { selected: true, control: true }), null);
  assert.equal(resolveKey({ key: "ArrowDown", shiftKey: true }, canvas), null);
});

test("? opens the help from the canvas, never from a text control", () => {
  assert.equal(resolveKey({ key: "?", shiftKey: true }, canvas), "help");
  assert.equal(resolveKey({ key: "?", shiftKey: true }, {}), "help");
  assert.equal(resolveKey({ key: "?" }, inField), null);
});

test("AltGraph and IME composition never act", () => {
  const altGr = (key, extra = {}) => ({ key, ctrlKey: true, altKey: true, getModifierState: (m) => m === "AltGraph", ...extra });
  assert.equal(resolveKey(altGr("s"), canvas), null);
  assert.equal(resolveKey(altGr("ArrowUp", { ctrlKey: false }), canvas), null);
  assert.equal(resolveKey({ key: "Delete", isComposing: true }, canvas), null);
  assert.equal(resolveKey({ key: "Process", keyCode: 229 }, canvas), null);
  assert.equal(resolveKey(mod("s", { isComposing: true }), inField), null);
});

test("Up and Down pick a field from the canvas when none is selected, and only there", () => {
  assert.equal(resolveKey({ key: "ArrowDown" }, { canvas: true }), "next");
  assert.equal(resolveKey({ key: "ArrowUp" }, { canvas: true }), "prev");
  assert.equal(resolveKey({ key: "ArrowDown" }, {}), null);
  assert.equal(resolveKey({ key: "Delete" }, { canvas: true }), null);
  assert.equal(resolveKey({ key: "ArrowDown" }, { canvas: true, control: true }), null);
});
