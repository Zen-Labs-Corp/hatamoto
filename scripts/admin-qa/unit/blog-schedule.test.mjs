// The blog editor's "Publish at" presets (UTC) and the picker button text.
// Run: node --test scripts/admin-qa/unit/blog-schedule.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/dates") return { url: ${JSON.stringify(new URL("../../../backend/web/admin/js/dates.js", import.meta.url).href)}, shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: "data:text/javascript,export const createAutosave=()=>({}),createRecovery=()=>({}),recoveryAction=()=>({});", shortCircuit: true };
      return next(s, c);
    }`),
);
const { schedulePresets, scheduleLabel } = await import("../../../backend/web/admin/js/blogeditor.js");

test("presets from a Friday afternoon", () => {
  const p = schedulePresets(new Date("2026-10-16T15:42:00Z"));
  assert.equal(p.now, "2026-10-16T15:42");
  assert.equal(p.tomorrow, "2026-10-17T09:00");
  assert.equal(p.monday, "2026-10-19T09:00");
  assert.equal(p.week, "2026-10-23T15:42");
  assert.equal(p.clear, "");
});

test("next Monday is a week away on a Monday and crosses month ends", () => {
  assert.equal(schedulePresets(new Date("2026-10-19T08:00:00Z")).monday, "2026-10-26T09:00");
  assert.equal(schedulePresets(new Date("2026-12-31T23:00:00Z")).tomorrow, "2027-01-01T09:00");
});

test("label", () => {
  assert.equal(scheduleLabel("", "UTC"), "Not scheduled");
  assert.equal(scheduleLabel("2026-10-17T09:00", "UTC"), "2026-10-17 09:00 UTC");
});

const { termSuggestions } = await import("../../../backend/web/admin/js/blogeditor.js");
const opts = [{ id: "1", name: "News" }, { id: "2", name: "Newsletter" }, { id: "3", name: "Go" }];

test("suggestions: contains, not already chosen, create only when new and allowed", () => {
  assert.deepEqual(termSuggestions(opts, new Set(["1"]), "new", false).map((o) => o.name), ["Newsletter"]);
  assert.deepEqual(termSuggestions(opts, new Set(), "ne", true).map((o) => o.name), ["News", "Newsletter", "ne"]);
  assert.equal(termSuggestions(opts, new Set(), "go", true).some((o) => o.create), false);
  assert.equal(termSuggestions(opts, new Set(), "zzz", false).length, 0);
  assert.equal(termSuggestions(opts, new Set(), "", true).length, 3);
});
