// Which sections the form inspector shows per type, and the label-to-name ops. Run: node --test scripts/admin-qa/unit/forms-inspector.test.mjs
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
const { sectionsFor } = await import(new URL("forms/inspsections.js", root));
const { compatible, TYPES } = await import(new URL("forms/types.js", root));
const { labelOps, applyOp } = await import(new URL("forms/ops.js", root));

test("text types have no Choices; choice types do", () => {
  for (const t of ["text", "email", "textarea", "number"]) assert.ok(!sectionsFor({ type: t }).includes("choices"), t);
  for (const t of ["select", "radio", "checkbox"]) assert.ok(sectionsFor({ type: t }).includes("choices"), t);
});

test("layout types have only Content", () => {
  for (const t of ["heading", "paragraph", "divider"]) assert.deepEqual(sectionsFor({ type: t }), ["content"]);
});

test("inputs get rules and advanced; hidden has no rules or layout", () => {
  assert.deepEqual(sectionsFor({ type: "email" }), ["content", "layout", "rules", "advanced"]);
  assert.deepEqual(sectionsFor({ type: "hidden" }), ["content", "advanced"]);
  assert.deepEqual(sectionsFor({ type: "consent" }), ["content", "layout", "rules"]);
});

test("the type switcher offers only the same family", () => {
  assert.deepEqual(compatible("select"), ["select", "radio", "checkbox"]);
  assert.deepEqual(compatible("heading"), ["heading", "paragraph"]);
  assert.ok(compatible("email").every((t) => TYPES[t].family === "input"));
  assert.ok(!compatible("email").includes("select"));
});

test("a label sets the name until the name is pinned", () => {
  const doc = { fields: [{ id: "a", type: "text", label: "Name", name: "name" }, { id: "b", type: "text", label: "X", name: "x" }, { id: "h", type: "heading", label: "", name: "" }] };
  const run = (ops) => ops.reduce((d, op) => applyOp(d, op).doc, doc);
  assert.equal(run(labelOps(doc, "b", "Full name")).fields[1].name, "full_name");
  assert.equal(run(labelOps(doc, "b", "Name")).fields[1].name, "name_2");
  assert.equal(labelOps({ fields: [{ ...doc.fields[1], name_locked: true }] }, "b", "Other").length, 1);
  assert.equal(labelOps(doc, "h", "Hello").length, 1);
});
