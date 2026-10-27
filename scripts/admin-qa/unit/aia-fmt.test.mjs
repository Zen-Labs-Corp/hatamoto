// AI analytics chart formatting. Run: node --test scripts/admin-qa/unit/aia-fmt.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const src = new URL("../../../backend/web/admin/js/aia/fmt.js", import.meta.url);
const { num, compact, percent, sharePercent, dayLabel, longDate, weekLabel, isoMonday } = await import(src);

test("imports in Node with no document or window", () => {
  assert.equal(typeof document, "undefined");
  assert.equal(typeof window, "undefined");
});

test("num groups thousands and rounds", () => {
  assert.equal(num(0), "0");
  assert.equal(num(999), "999");
  assert.equal(num(1000), "1,000");
  assert.equal(num(55574.4), "55,574");
  assert.equal(num(2_500_000), "2,500,000");
});

test("compact is the axis style", () => {
  assert.equal(compact(0), "0");
  assert.equal(compact(999), "999");
  assert.equal(compact(1000), "1K");
  assert.equal(compact(1234), "1.2K");
  assert.equal(compact(12_345), "12K");
  assert.equal(compact(2_500_000), "2.5M");
  assert.equal(compact(0.25), "0.3");
});

test("percent and the share rule flip at 10", () => {
  assert.equal(percent(3.84), "3.8%");
  assert.equal(percent(81, 0), "81%");
  assert.equal(sharePercent(9.94), "9.9%");
  assert.equal(sharePercent(9.96), "10%");
  assert.equal(sharePercent(10.4), "10%");
  assert.equal(sharePercent(0), "0.0%");
});

test("date labels use Sep and UTC", () => {
  assert.equal(dayLabel("2026-10-23"), "23 Oct");
  assert.equal(dayLabel("2026-09-01"), "1 Sep");
  assert.equal(longDate("2026-10-23"), "23 Oct 2026");
  assert.equal(longDate("2025-12-31"), "31 Dec 2025");
});

test("isoMonday: Sunday belongs to the week before, across month and year", () => {
  assert.equal(isoMonday("2026-10-25"), "2026-10-19"); // Sunday
  assert.equal(isoMonday("2026-10-19"), "2026-10-19"); // Monday
  assert.equal(isoMonday("2026-10-21"), "2026-10-19");
  assert.equal(isoMonday("2026-11-01"), "2026-10-26"); // month boundary
  assert.equal(isoMonday("2026-01-01"), "2025-12-29"); // year boundary
});

test("weekLabel names the week by its Monday", () => {
  assert.equal(weekLabel("2026-10-20"), "Week of 19 Oct");
  assert.equal(weekLabel("2026-10-19"), "Week of 19 Oct");
  assert.equal(weekLabel("2026-10-25"), "Week of 19 Oct");
  assert.equal(weekLabel("2026-01-01"), "Week of 29 Dec");
});
