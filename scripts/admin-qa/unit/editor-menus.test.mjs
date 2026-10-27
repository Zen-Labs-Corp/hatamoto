// Pure parts of the editor bubble and slash menus. The modules import Tiptap and the
// @hm import map, which Node cannot resolve, so a resolver hook swaps them for stubs.
// Run: node --test scripts/admin-qa/unit/editor-menus.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/editor/", import.meta.url);
const stub = "data:text/javascript,export default {}; export const Extension={create:()=>({})}; export const PluginKey=class{}; export const TextSelection={};";
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@tiptap/")) return { url: ${JSON.stringify(stub)}, shortCircuit: true };
      if (s === "@hm/editor/slash") return { url: ${JSON.stringify(new URL("slash.js", root).href)}, shortCircuit: true };
      return next(s, c);
    }`),
);
const { SLASH_COMMANDS, SLASH_GROUPS, filterCommands, groupCommands } = await import(new URL("slash.js", root));
const { BUBBLE_ITEMS } = await import(new URL("bubble.js", root));

test("every slash command has a group, icon and label", () => {
  assert.equal(SLASH_COMMANDS.length, 16);
  for (const c of SLASH_COMMANDS) {
    assert.ok(SLASH_GROUPS.includes(c.group), c.id);
    assert.ok(c.icon, c.id);
    assert.equal(typeof c.hint, "string", c.id);
  }
});

test("filtering by ta puts Table first", () => {
  assert.equal(filterCommands("ta")[0].id, "table");
});

test("groupCommands keeps each group contiguous", () => {
  const seen = [];
  for (const c of groupCommands(SLASH_COMMANDS)) if (seen.at(-1) !== c.group) seen.push(c.group);
  assert.deepEqual(seen, SLASH_GROUPS);
});

test("bubble order and labels", () => {
  assert.deepEqual(BUBBLE_ITEMS.map((i) => i.title), ["Bold", "Italic", "Inline code", "Link"]);
  assert.ok(BUBBLE_ITEMS[3].separatorBefore);
});

test("the blog editor mounts a hidden toolbar and the kit can show it", async () => {
  const { readFileSync } = await import("node:fs");
  const blog = readFileSync(new URL("../blogeditor.js", root), "utf8");
  const kit = readFileSync(new URL("kit.js", root), "utf8");
  assert.match(blog, /bar: "hidden"/);
  assert.match(blog, /altKey && e\.shiftKey && !mod && \(e\.code === "KeyT" \|\| e\.key\?\.toLowerCase\(\) === "t"\)/); // Ctrl+Shift+T is the browsers' own
  assert.match(kit, /setBar\(on\)/);
});

test("sidebar tabs move with the arrow keys and wrap", async () => {
  const stub2 = "data:text/javascript,export const createAutosave=()=>{};export const createRecovery=()=>{};export const recoveryAction=()=>{};";
  register("data:text/javascript," + encodeURIComponent(`export async function resolve(s, c, next) { if (s === "@hm/dates") return { url: ${JSON.stringify(new URL("../dates.js", root).href)}, shortCircuit: true }; if (s === "@hm/editor/autosave") return { url: ${JSON.stringify(stub2)}, shortCircuit: true }; return next(s, c); }`));
  const { tabTarget } = await import(new URL("../blogeditor.js", root));
  assert.equal(tabTarget(0, "ArrowRight", 2), 1);
  assert.equal(tabTarget(1, "ArrowRight", 2), 0);
  assert.equal(tabTarget(0, "ArrowLeft", 2), 1);
  assert.equal(tabTarget(1, "Home", 2), 0);
  assert.equal(tabTarget(0, "End", 2), 1);
  assert.equal(tabTarget(0, "Tab", 2), null);
});
