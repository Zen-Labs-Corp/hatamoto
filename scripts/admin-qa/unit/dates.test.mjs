// The client-side date helpers match the server's day-month format (format.go), including "Sep".
// Run: node --test scripts/admin-qa/unit/dates.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { hmDate, hmTime, hmDateTime, shortMonth } = await import(new URL("../../../backend/web/admin/js/dates.js", import.meta.url));

test("September is Sep, as in Go", () => {
  const d = new Date(2026, 8, 17, 9, 5);
  assert.equal(hmDate(d), "17 Sep 2026");
  assert.equal(hmTime(d), "09:05");
  assert.equal(hmDateTime(d), "17 Sep 2026 09:05");
  assert.equal(shortMonth("17 Sept"), "17 Sep");
});

test("other months and the 24h clock are unchanged", () => {
  const d = new Date(2026, 0, 2, 15, 4);
  assert.equal(hmDateTime(d), "2 Jan 2026 15:04");
});
