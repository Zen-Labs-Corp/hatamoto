// Leaving the designer by a link. Run: node --test scripts/admin-qa/unit/forms-leave.test.mjs
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
const { leaveMode, afterLeave } = await import(new URL("forms/leave.js", root));

test("a link is followed at once when nothing is unsaved, or saving is stopped", () => {
  assert.equal(leaveMode({ dirty: false, state: "saved" }), "go");
  assert.equal(leaveMode({ dirty: true, state: "conflict" }), "go");
  assert.equal(leaveMode({ dirty: true, state: "stopped" }), "go");
});

test("unsaved changes hold the click for a save, in every state a save can run", () => {
  for (const state of ["pending", "saving", "error", "idle"]) assert.equal(leaveMode({ dirty: true, state }), "save");
});

test("after the save: clean goes, a failed save asks (422 and network errors), a fresh stop stays", () => {
  assert.equal(afterLeave({ dirty: false, state: "saved" }), "go");
  assert.equal(afterLeave({ dirty: true, state: "error" }), "ask");
  assert.equal(afterLeave({ dirty: true, state: "conflict" }), "stay");
  assert.equal(afterLeave({ dirty: true, state: "stopped" }), "stay");
});
