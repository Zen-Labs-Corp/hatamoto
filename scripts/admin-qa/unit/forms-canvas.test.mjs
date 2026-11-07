// The designer canvas's pure helpers. Run: node --test scripts/admin-qa/unit/forms-canvas.test.mjs
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
const { fieldSummary, ruleNote } = await import(new URL("forms/canvas.js", root));

const email = { id: "f_1", type: "email", label: "Email", name: "email" };

test("fieldSummary names the type and flags a required input", () => {
  assert.equal(fieldSummary({ id: "a", type: "text", label: "Name", name: "name" }), "Short text");
  assert.equal(fieldSummary({ id: "a", type: "text", label: "Name", name: "name", required: true }), "Short text · required");
  assert.equal(fieldSummary({ id: "h", type: "heading", required: true }), "Heading");
});

test("a rule reads as a sentence, naming the source field", () => {
  const f = { id: "a", type: "text", label: "Why", name: "why", logic: { field_id: "f_1", op: "filled" } };
  assert.equal(ruleNote(f, [email, f]), "Shows when Email is filled");
  assert.equal(ruleNote({ ...f, logic: { field_id: "f_1", op: "is", value: "x@y.z" } }, [email]), 'Shows when Email is "x@y.z"');
  assert.equal(ruleNote({ ...f, logic: { field_id: "gone", op: "empty" } }, [email]), "Shows when a removed field is empty");
  assert.equal(ruleNote(email, []), "");
  assert.match(fieldSummary(f, [email, f]), /Short text · Shows when Email is filled/);
});
