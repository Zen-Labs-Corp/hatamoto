// History labels derived from the ops and the pre-commit document.
// Run: node --test scripts/admin-qa/unit/builder-historylabel.test.mjs
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
const { derive, isGeneric } = await import(new URL("builder/historylabel.js", root));
const { makeKindNames } = await import(new URL("builder/kindnames.js", root));
const S = await import(new URL("builder/store.js", root));

const kindName = makeKindNames();
const before = { v: 2, nodes: [{ id: "c", t: "container", c: [{ id: "b", t: "button", p: {} }] }, { id: "h", t: "heading", p: { text: "x" } }] };
const d = (ops, label = "") => derive(ops, before, { kindName, label });

const cases = [
  ["add", [{ op: "insert", parent: "", index: 0, node: { id: "n", t: "heading" } }], "Add", "Add Heading"],
  ["duplicate", [{ op: "insert", parent: "", index: 1, node: { id: "n", t: "button" } }], "Duplicate", "Duplicate Button"],
  ["duplicate many", [{ op: "insert", parent: "", index: 1, node: { id: "n" } }, { op: "insert", parent: "", index: 2, node: { id: "m" } }], "Duplicate", "Duplicate 2 elements"],
  ["delete", [{ op: "remove", id: "c" }], "Remove", "Delete Container"],
  ["delete many", [{ op: "remove", id: "c" }, { op: "remove", id: "h" }], "Delete", "Delete 2 elements"],
  ["move", [{ op: "move", id: "b", parent: "", index: 0 }], "Move", "Move Button"],
  ["style desktop", [{ op: "setStyle", id: "h", bp: "base", key: "color", value: "x" }], "Style", "Change Text colour"],
  ["style tablet", [{ op: "setStyle", id: "h", bp: "md", key: "color", value: "x" }], "Style", "Change Text colour (Tablet)"],
  ["style many keys", [{ op: "setStyle", id: "h", bp: "base", key: "pt", value: "1" }, { op: "setStyle", id: "h", bp: "base", key: "pb", value: "1" }], "Style", "Change style"],
  ["prop", [{ op: "setProp", id: "h", key: "text", value: "y" }], "Edit text", "Change text"],
  ["prop key", [{ op: "setProp", id: "h", key: "alt", value: "y" }], "Edit alt", "Change alt"],
];
for (const [name, ops, label, want] of cases) test(`derive: ${name}`, () => assert.equal(d(ops, label), want));

test("isGeneric: verbs and Edit <key> are replaced, specific labels stay", () => {
  for (const l of ["", "Add", "Move", "Remove", "Style", "Change", "Duplicate", "Edit text", "Edit href"]) assert.equal(isGeneric(l), true, l);
  for (const l of ["Paste 3 elements", "Wrap in Container", "Rename", "Add Heading", "Change Font size (Tablet)", "Change Link", "Bind Text", "Change Number of columns"]) assert.equal(isGeneric(l), false, l);
});

test("a derived label lands in the history entry", () => {
  const ops = [{ op: "remove", id: "h" }];
  const s = S.commit(S.createStore(before), ops, { label: d(ops, "Remove") });
  assert.equal(s.past.at(-1).label, "Delete Heading");
});
