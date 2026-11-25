// A query loop's sort and filter helpers (pure): operators per type, value kinds, filter cleaning.
// Run: node --test scripts/admin-qa/unit/builder-loopattrs.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { MAX_FILTERS, attrsFor, cleanFilters, opsFor, sortable, staleAfterSchemaChange, valueKind } from "../../../backend/web/admin/js/builder/loopattrs.js";

const attrs = [
  { key: "start_date", label: "Start date", type: "date" },
  { key: "capacity", label: "Capacity", type: "number" },
  { key: "status", label: "Status", type: "enum", enum: ["scheduled", "cancelled"] },
  { key: "venue", label: "Venue", type: "text" },
];

test("opsFor follows the attribute type", () => {
  assert.deepEqual(opsFor("date").map((o) => o[0]), ["from_today", "before_today", "next_days"]);
  assert.deepEqual(opsFor("number").map((o) => o[0]), ["eq", "lt", "lte", "gt", "gte"]);
  assert.deepEqual(opsFor("enum").map((o) => o[0]), ["eq", "ne"]);
  assert.deepEqual(opsFor("text").map((o) => o[0]), ["eq", "ne"]);
  assert.deepEqual(opsFor("nope"), []);
});

test("valueKind names the control an operator needs", () => {
  assert.equal(valueKind("date", "from_today"), "none");
  assert.equal(valueKind("date", "before_today"), "none");
  assert.equal(valueKind("date", "next_days"), "days");
  assert.equal(valueKind("number", "gte"), "number");
  assert.equal(valueKind("enum", "ne"), "select");
  assert.equal(valueKind("text", "eq"), "text");
});

test("attrsFor is empty for an unknown schema; sortable keeps the usable types", () => {
  assert.deepEqual(attrsFor({ event: attrs }, "event"), attrs);
  assert.deepEqual(attrsFor({ event: attrs }, "post"), []);
  assert.deepEqual(attrsFor(undefined, "event"), []);
  assert.equal(sortable([...attrs, { key: "x", type: "blob" }]).length, 4);
});

test("cleanFilters shapes values to the loop schema", () => {
  const got = cleanFilters(
    [
      { attr: "start_date", op: "from_today", value: "ignored" },
      { attr: "capacity", op: "gte", value: "10" },
      { attr: "status", op: "ne", value: "cancelled" },
    ],
    attrs,
  );
  assert.deepEqual(got, [
    { attr: "start_date", op: "from_today" },
    { attr: "capacity", op: "gte", value: 10 },
    { attr: "status", op: "ne", value: "cancelled" },
  ]);
});

test("cleanFilters drops incomplete or wrong rows", () => {
  const rows = [
    { attr: "capacity", op: "gte", value: "" },
    { attr: "capacity", op: "gte", value: "abc" },
    { attr: "venue", op: "eq", value: "" },
    { attr: "venue", op: "from_today" },
    { attr: "gone", op: "eq", value: "x" },
    { attr: "start_date", op: "next_days" },
    { attr: "venue", op: "eq", value: "x".repeat(201) },
  ];
  assert.equal(cleanFilters(rows, attrs), null);
  assert.equal(cleanFilters([], attrs), null);
  assert.equal(cleanFilters(undefined, attrs), null);
});

test("next_days takes a whole number of days from 0 to 3650", () => {
  const f = (days) => cleanFilters([{ attr: "start_date", op: "next_days", days }], attrs);
  assert.deepEqual(f("0"), [{ attr: "start_date", op: "next_days", days: 0 }]);
  assert.deepEqual(f(3650), [{ attr: "start_date", op: "next_days", days: 3650 }]);
  assert.equal(f(3651), null);
  assert.equal(f(-1), null);
  assert.equal(f(1.5), null);
});

test("cleanFilters keeps at most three rows", () => {
  const rows = Array.from({ length: 5 }, () => ({ attr: "start_date", op: "from_today" }));
  assert.equal(cleanFilters(rows, attrs).length, MAX_FILTERS);
});

test("a schema or Context change with sort or filters set leaves stale rows to clear", () => {
  const set = { from: "query", schema: "event", order_by: { attr: "start_date", dir: "asc" } };
  assert.equal(staleAfterSchemaChange(set, { ...set, schema: "post" }), true);
  assert.equal(staleAfterSchemaChange(set, { ...set, context: true }), true);
  assert.equal(staleAfterSchemaChange({ schema: "event" }, { schema: "post", filters: [{ attr: "a", op: "eq", value: "x" }] }), true);
});

test("nothing to clear when the schema is the same, nothing is set, or the loop is not a query loop", () => {
  const set = { from: "query", schema: "event", filters: [{ attr: "a", op: "eq", value: "x" }] };
  assert.equal(staleAfterSchemaChange(set, { ...set, limit: 3 }), false);
  assert.equal(staleAfterSchemaChange({ schema: "event" }, { schema: "post" }), false);
  assert.equal(staleAfterSchemaChange({ from: "source", schema: "event" }, { from: "source", schema: "post" }), false);
});

test("leaving a query loop (From to source or archive) leaves sort and filters that Validate refuses", () => {
  const set = { from: "query", schema: "event", filters: [{ attr: "a", op: "eq", value: "x" }] };
  assert.equal(staleAfterSchemaChange(set, { ...set, from: "source" }), true);
  assert.equal(staleAfterSchemaChange(set, { ...set, from: "archive" }), true);
  assert.equal(staleAfterSchemaChange({ schema: "event", order_by: { attr: "a", dir: "asc" } }, { schema: "event", from: "source" }), true);
});
