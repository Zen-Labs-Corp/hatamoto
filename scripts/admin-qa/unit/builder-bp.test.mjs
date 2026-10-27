// Device labels with real widths. Run: node --test scripts/admin-qa/unit/builder-bp.test.mjs
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

const { BP_NAME, BREAKPOINT_MAX, DEVICE_LABEL } = await import("@hm/builder/bp");

test("device labels carry the real widths", () => {
  assert.deepEqual(BREAKPOINT_MAX, { md: 1024, sm: 640 });
  assert.deepEqual(DEVICE_LABEL, { base: "Desktop", md: "Tablet ≤1024", sm: "Phone ≤640" });
});

test("BP_NAME is unchanged", () => {
  assert.deepEqual(BP_NAME, { base: "Desktop", md: "Tablet", sm: "Phone" });
});
