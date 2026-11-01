// The keyboard table and the navigator's pure model. The @hm specifiers resolve to the
// real modules (all three are DOM-free at import time).
// Run: node --test scripts/admin-qa/unit/builder-keys.test.mjs
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
const { SHORTCUTS, GROUPS, filterGroups } = await import(new URL("../../../backend/web/admin/js/builder/keys.js", import.meta.url));
const { resolveKey } = await import(new URL("../../../backend/web/admin/js/builder/keymap.js", import.meta.url));
const { rowsOf, step, moveOp, label, rowName } = await import(new URL("../../../backend/web/admin/js/builder/navigator.js", import.meta.url));

const tree = { tree: true, free: true, selected: true };
const free = { free: true, selected: true };
const text = { text: true, free: false, selected: true };
const k = (key, extra = {}) => ({ key, ...extra });

test("tree keys", () => {
  const table = [
    ["ArrowUp", "prev"], ["ArrowDown", "next"], ["ArrowLeft", "collapse"], ["ArrowRight", "expand"],
    ["Home", "first"], ["End", "last"], ["Enter", "inspect"], ["Delete", "remove"], ["Backspace", "remove"],
  ];
  for (const [key, action] of table) assert.equal(resolveKey(k(key), tree), action, key);
  for (const [key, action] of [["ArrowUp", "moveup"], ["ArrowDown", "movedown"], ["ArrowLeft", "outdent"], ["ArrowRight", "indent"]]) {
    assert.equal(resolveKey(k(key, { altKey: true }), tree), action, "alt " + key);
  }
});

test("tree navigation needs the tree; Alt+Up/Down and Backspace also work on the page; everything needs a selection", () => {
  assert.equal(resolveKey(k("ArrowDown"), free), "nextsection", "on the page Down steps sections, not tree rows");
  assert.equal(resolveKey(k("ArrowDown"), { free: true }), null, "nothing selected: the page scrolls");
  assert.equal(resolveKey(k("ArrowDown", { altKey: true }), free), "movedown");
  assert.equal(resolveKey(k("ArrowUp", { altKey: true }), free), "moveup");
  assert.equal(resolveKey(k("ArrowLeft", { altKey: true }), free), null, "indent and outdent stay the navigator's");
  assert.equal(resolveKey(k("ArrowRight", { altKey: true }), free), null);
  assert.equal(resolveKey(k("ArrowDown", { altKey: true }), { free: true }), null);
  assert.equal(resolveKey(k("Delete"), { tree: true, free: true, selected: false }), null);
  assert.equal(resolveKey(k("Backspace"), free), "remove");
  assert.equal(resolveKey(k("Backspace"), { free: true }), null);
});

test("global keys", () => {
  assert.equal(resolveKey(k("Delete"), free), "remove");
  assert.equal(resolveKey(k("d", { ctrlKey: true }), free), "duplicate");
  assert.equal(resolveKey(k("D", { metaKey: true }), free), "duplicate");
  assert.equal(resolveKey(k("/"), free), "palette");
  assert.equal(resolveKey(k("a"), free), "palette");
  assert.equal(resolveKey(k("?", { shiftKey: true }), free), "sheet");
  assert.equal(resolveKey(k("z", { ctrlKey: true }), free), "undo");
  assert.equal(resolveKey(k("Z", { ctrlKey: true, shiftKey: true }), free), "redo");
  assert.equal(resolveKey(k("y", { metaKey: true }), free), "redo");
  assert.equal(resolveKey(k("y", { ctrlKey: true, shiftKey: true }), free), null);
});

test("controls keep their keys", () => {
  for (const key of ["a", "/", "?", "Delete", "Backspace", "ArrowDown", "Enter"]) assert.equal(resolveKey(k(key), text), null, key);
  for (const key of ["z", "y", "d"]) assert.equal(resolveKey(k(key, { ctrlKey: true }), text), null, "ctrl " + key);
  assert.equal(resolveKey(k("s", { ctrlKey: true }), text), "save");
});

test("focus outside the free zones (toolbar button, dialog) takes nothing but undo and save", () => {
  const other = { free: false, selected: true };
  assert.equal(resolveKey(k("a"), other), null);
  assert.equal(resolveKey(k("Delete"), other), null);
  assert.equal(resolveKey(k("z", { ctrlKey: true }), other), "undo");
  assert.equal(resolveKey(k("s", { metaKey: true }), other), "save");
});

test("every shortcut in the sheet is a row with text", () => {
  assert.ok(SHORTCUTS.length >= 12);
  for (const [a, b] of SHORTCUTS) assert.ok(a && b);
});

const leaf = (id, t = "text", p = { text: id }) => ({ id, t, p });
const box = (id, c = [], p) => ({ id, t: "container", ...(p ? { p } : {}), ...(c.length ? { c } : {}) });
const doc = () => ({ v: 2, nodes: [box("a", [leaf("a1"), box("a2", [leaf("a21")])]), box("b"), leaf("c", "heading", { text: "Hello", level: 2 })] });

test("rowsOf: levels, positions, expanded, collapsed subtrees hidden", () => {
  const rows = rowsOf(doc());
  assert.deepEqual(rows.map((r) => [r.id, r.level, r.pos, r.size]), [["a", 1, 1, 3], ["a1", 2, 1, 2], ["a2", 2, 2, 2], ["a21", 3, 1, 1], ["b", 1, 2, 3], ["c", 1, 3, 3]]);
  assert.equal(rows[0].expanded, true);
  assert.equal(rows[4].expanded, false); // no kids: not expandable
  assert.equal(rows[4].kids, 0);
  const closed = rowsOf(doc(), new Set(["a"]));
  assert.deepEqual(closed.map((r) => r.id), ["a", "b", "c"]);
  assert.equal(closed[0].expanded, false);
  assert.equal(closed[0].kids, 2);
});

test("step: arrows, fold and parent/child moves", () => {
  const rows = rowsOf(doc());
  assert.deepEqual(step(rows, "a", "next"), { select: "a1" });
  assert.equal(step(rows, "a", "prev"), null);
  assert.deepEqual(step(rows, "c", "first"), { select: "a" });
  assert.deepEqual(step(rows, "a", "last"), { select: "c" });
  assert.deepEqual(step(rows, "a", "collapse"), { collapse: "a" });
  assert.deepEqual(step(rows, "a1", "collapse"), { select: "a" });
  assert.equal(step(rows, "b", "collapse"), null);
  assert.deepEqual(step(rows, "a", "expand"), { select: "a1" });
  const closed = rowsOf(doc(), new Set(["a"]));
  assert.deepEqual(step(closed, "a", "expand"), { expand: "a" });
  assert.equal(step(closed, "b", "expand"), null);
  assert.deepEqual(step(rows, "gone", "next"), { select: "a" });
});

test("moveOp: siblings, outdent, indent", () => {
  const d = doc();
  assert.deepEqual(moveOp(d, "a1", "movedown"), { op: "move", id: "a1", parent: "a", index: 1 });
  assert.equal(moveOp(d, "a1", "moveup"), null);
  assert.equal(moveOp(d, "a2", "movedown"), null);
  assert.deepEqual(moveOp(d, "b", "moveup"), { op: "move", id: "b", parent: "", index: 0 });
  assert.deepEqual(moveOp(d, "a21", "outdent"), { op: "move", id: "a21", parent: "a", index: 2 });
  assert.deepEqual(moveOp(d, "a1", "outdent"), { op: "move", id: "a1", parent: "", index: 1 });
  assert.equal(moveOp(d, "a", "outdent"), null);
  assert.deepEqual(moveOp(d, "b", "indent"), { op: "move", id: "b", parent: "a", index: 2 });
  assert.equal(moveOp(d, "a", "indent"), null); // no previous sibling
  assert.deepEqual(moveOp(d, "c", "indent"), { op: "move", id: "c", parent: "b", index: 0 });
  assert.equal(moveOp(d, "a21", "indent"), null); // the previous sibling is not there
});

test("label", () => {
  assert.equal(label(leaf("x", "heading", { text: "Hello", level: 2 })), "Heading · Hello");
  assert.equal(label({ id: "s", t: "container", p: { tag: "section" } }), "Section");
  assert.equal(label({ id: "g", t: "container", p: { layout: "grid" } }), "Grid");
  assert.equal(label({ id: "k", t: "component", p: { type: "hero" } }), "Hero");
  assert.equal(label({ id: "i", t: "image" }), "Image");
})

test("label: a navlabel names any kind first, blank falls back", () => {
  assert.equal(label({ id: "s", t: "container", p: { tag: "section", navlabel: "  Intro " } }), "Intro");
  assert.equal(label(leaf("x", "heading", { text: "Hello", navlabel: "Title" })), "Title");
  assert.equal(label({ id: "k", t: "component", p: { type: "hero", navlabel: "Top" } }), "Top");
  assert.equal(label(leaf("x", "heading", { text: "Hello", navlabel: "   " })), "Heading · Hello");
  assert.equal(rowName({ id: "s", t: "container", p: { navlabel: "Intro" } }, "preset card"), "Intro, preset card");
});

test("copy and paste style keys: Ctrl/Cmd+Alt+C and V, never plain Ctrl+C/V, never in text controls", () => {
  for (const mod of [{ ctrlKey: true }, { metaKey: true }]) {
    assert.equal(resolveKey(k("c", { ...mod, altKey: true }), tree), "copystyle");
    assert.equal(resolveKey(k("v", { ...mod, altKey: true }), free), "pastestyle");
    assert.equal(resolveKey(k("ç", { ...mod, altKey: true, code: "KeyC" }), tree), "copystyle"); // Alt remaps the character
    assert.equal(resolveKey(k("c", { ...mod, altKey: true }), text), null);
  }
  assert.equal(resolveKey(k("C", { ctrlKey: true, shiftKey: true }), free), null);
  assert.equal(resolveKey(k("c", { ctrlKey: true, altKey: true }), { free: true, selected: false }), null);
  assert.ok(SHORTCUTS.some(([keys]) => keys.includes("Alt + C")) && SHORTCUTS.some(([keys]) => keys.includes("Alt + V")));
});

test("node clipboard, wrap, select siblings and history keys", () => {
  const table = [["c", "copynode"], ["x", "cutnode"], ["v", "pastenode"], ["g", "wrap"], ["G", "unwrap"], ["a", "selectsiblings"], ["h", "history"]];
  for (const ctx of [tree, free]) {
    for (const [key, action] of table) {
      const shift = key === "G";
      assert.equal(resolveKey(k(key, { ctrlKey: true, shiftKey: shift }), ctx), action, key);
      assert.equal(resolveKey(k(key, { metaKey: true, shiftKey: shift }), ctx), action, "meta " + key);
      const none = resolveKey(k(key, { ctrlKey: true, shiftKey: shift }), { ...ctx, selected: false });
      assert.equal(none, key === "v" || key === "h" ? action : null, "needs a selection, except paste (empty page) and history " + key);
      assert.equal(resolveKey(k(key, { ctrlKey: true, shiftKey: shift }), { ...ctx, free: false, tree: false }), null, "outside the free zones " + key);
    }
  }
  // text controls keep their own clipboard, select all, history
  for (const [key] of table) assert.equal(resolveKey(k(key, { ctrlKey: true }), text), null, "text " + key);
  // a text selection on the page keeps copy, cut and select all for the browser; paste, wrap and history still work
  const range = { ...tree, range: true };
  for (const key of ["c", "x", "a"]) assert.equal(resolveKey(k(key, { ctrlKey: true }), range), null, "range " + key);
  for (const [key, action] of [["v", "pastenode"], ["g", "wrap"], ["h", "history"]]) assert.equal(resolveKey(k(key, { ctrlKey: true }), range), action, "range " + key);
  // Ctrl+Alt+C/V stay style; Ctrl+Shift+C/V/A are the browser's
  assert.equal(resolveKey(k("c", { ctrlKey: true, altKey: true }), tree), "copystyle");
  assert.equal(resolveKey(k("v", { ctrlKey: true, altKey: true }), tree), "pastestyle");
  for (const key of ["C", "V", "A"]) assert.equal(resolveKey(k(key, { ctrlKey: true, shiftKey: true }), tree), null, "shift " + key);
  for (const key of ["c", "x", "v", "g", "a", "h"]) assert.equal(resolveKey(k(key), tree), key === "a" ? "palette" : null, "plain " + key);
});

test("F2 renames in the tree, Shift+F10 and the menu key open the menu; none in a text control", () => {
  assert.equal(resolveKey(k("F2"), tree), "rename");
  assert.equal(resolveKey(k("F2"), free), null);
  assert.equal(resolveKey(k("F2"), { ...tree, selected: false }), null);
  assert.equal(resolveKey(k("F2"), text), null);
  for (const ctx of [tree, free]) {
    assert.equal(resolveKey(k("F10", { shiftKey: true }), ctx), "menu");
    assert.equal(resolveKey(k("ContextMenu"), ctx), "menu");
    assert.equal(resolveKey(k("F10", { shiftKey: true }), { ...ctx, selected: false }), null);
  }
  assert.equal(resolveKey(k("F10"), tree), null);
  assert.equal(resolveKey(k("F10", { shiftKey: true }), text), null);
  assert.equal(resolveKey(k("ContextMenu"), { free: false, selected: true }), null);
});

test("the sheet lists the node clipboard, wrap, rename and menu keys", () => {
  for (const needle of ["Ctrl/Cmd + C / X / V", "Ctrl/Cmd + G", "Ctrl/Cmd + Shift + G", "Ctrl/Cmd + A", "Ctrl/Cmd + H", "F2", "Shift + F10"]) assert.ok(SHORTCUTS.some(([keys]) => keys.includes(needle)), needle);
});

test("rowName: label plus the preset note, no extra text without one", () => {
  const n = { id: "a", t: "container" };
  assert.equal(rowName(n), label(n));
  assert.equal(rowName(n, "preset card, differs"), `${label(n)}, preset card, differs`);
});

test("pane toggles: Ctrl/Cmd+Alt+[ and ] work from anywhere, plain brackets do not", () => {
  const text = { text: true };
  for (const ctx of [free, text, {}]) {
    assert.equal(resolveKey({ key: "[", code: "BracketLeft", ctrlKey: true, altKey: true }, ctx), "togglenav");
    assert.equal(resolveKey({ key: "“", code: "BracketLeft", metaKey: true, altKey: true }, ctx), "togglenav");
    assert.equal(resolveKey({ key: "]", code: "BracketRight", ctrlKey: true, altKey: true }, ctx), "toggleinspector");
  }
  assert.equal(resolveKey({ key: "[", code: "BracketLeft", ctrlKey: true }, free), null);
  assert.equal(resolveKey({ key: "[", code: "BracketLeft" }, free), null);
  assert.ok(SHORTCUTS.some(([keys]) => keys.includes("[")));
});

test("pane toggles: physical bracket keys only, AltGr brackets are typed", () => {
  const mods = { ctrlKey: true, altKey: true };
  assert.equal(resolveKey({ key: "[", code: "Digit8", ...mods }, text), null);
  assert.equal(resolveKey({ key: "]", code: "Digit9", ...mods }, text), null);
  assert.equal(resolveKey({ key: "[", code: "BracketLeft", ...mods, getModifierState: (m) => m === "AltGraph" }, text), null);
  assert.equal(resolveKey({ key: "“", code: "BracketLeft", ...mods }, text), "togglenav");
  assert.equal(resolveKey({ key: "]", code: "BracketRight", ctrlKey: true, altKey: true }, text), "toggleinspector");
  assert.equal(resolveKey({ key: "]", code: "BracketRight", metaKey: true, altKey: true }, free), "toggleinspector");
});

const OLD_KEYS = ["Up / Down", "Left / Right", "Home / End", "Alt + Up / Down", "Alt + Left", "Alt + Right", "H", "Delete", "Ctrl/Cmd + D", "Ctrl/Cmd + C / X / V", "Ctrl/Cmd + G", "Ctrl/Cmd + Shift + G", "Ctrl/Cmd + A", "Ctrl/Cmd + H", "F2", "Shift + F10 or Menu key", "Ctrl/Cmd + Alt + C", "Ctrl/Cmd + Alt + V", "Ctrl/Cmd + Z", "Ctrl/Cmd + Shift + Z or Ctrl + Y", "Ctrl/Cmd + S", "Ctrl/Cmd + Alt + [ / ]", "Left / Right on a pane edge", "Enter", "/ or A", "?"];

test("the sheet is grouped, keeps every old row and adds Ctrl/Cmd K, Shift+Enter, / and F2", () => {
  assert.deepEqual(GROUPS.map(([g]) => g), ["Add and insert", "Select and move", "Edit", "View and device", "History"]);
  const keys = SHORTCUTS.map(([a]) => a);
  for (const old of OLD_KEYS) assert.ok(keys.includes(old), old);
  for (const added of ["Ctrl/Cmd + K", "Shift + Enter in the Add search", "/ or A", "F2"]) assert.ok(keys.includes(added), added);
  assert.equal(new Set(keys).size, keys.length, "no row twice");
});

test("the sheet says Navigator, not the structure pane", () => {
  for (const [a, b] of SHORTCUTS) assert.ok(!/structure/i.test(a + b), b);
});

test("filterGroups matches key text and description, ignores case and drops empty groups", () => {
  assert.equal(filterGroups(GROUPS, ""), GROUPS);
  const undo = filterGroups(GROUPS, "UNDO");
  assert.deepEqual(undo.map(([g]) => g), ["History"]);
  assert.deepEqual(undo[0][1].map(([a]) => a), ["Ctrl/Cmd + Z"]);
  assert.deepEqual(filterGroups(GROUPS, "commands search").flatMap(([, r]) => r.map(([a]) => a)), ["Ctrl/Cmd + K"], "every word, in the description");
  assert.deepEqual(filterGroups(GROUPS, "shift enter keep").flatMap(([, r]) => r.map(([a]) => a)), ["Shift + Enter in the Add search"]);
  assert.deepEqual(filterGroups(GROUPS, "zzzz"), []);
});

test("the sheet's scrolling list is a named region with a Tab stop (axe scrollable-region-focusable)", async () => {
  const { readFile } = await import("node:fs/promises");
  const src = await readFile(new URL("builder/keys.js", root), "utf8");
  const body = src.match(/h\("div", \{[^}]*id: "hm-bs-sheet-body"[^}]*\}/)?.[0] ?? "";
  assert.match(body, /tabindex: "0"/);
  assert.match(body, /role: "region"/);
  assert.match(body, /"aria-label": "[^"]+"/);
});
