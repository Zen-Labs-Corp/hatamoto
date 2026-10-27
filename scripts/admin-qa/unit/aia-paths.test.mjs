// AI analytics chart path geometry. Run: node --test scripts/admin-qa/unit/aia-paths.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const src = new URL("../../../backend/web/admin/js/aia/paths.js", import.meta.url);
const { linePath, areaPath, stackBaselines, columns, columnWidth, markerIndexes, sparkPoints, OPACITY, STROKE } = await import(src);

test("imports in Node with no document or window", () => {
  assert.equal(typeof document, "undefined");
  assert.equal(typeof window, "undefined");
});

test("prototype constants", () => {
  assert.deepEqual(OPACITY, { stacked: 0.3, single: 0.14, spark: 0.14, compareTotal: 0.45, compareSeries: 0.6 });
  assert.equal(STROKE.line, 1.6);
  assert.equal(STROKE.compare, 1.25);
});

test("linePath and areaPath for a 3-point series", () => {
  const xs = [46, 200.55, 355];
  assert.equal(linePath(xs, [100, 50.25, 80]), "M46.0,100.0 L200.6,50.3 L355.0,80.0");
  assert.equal(areaPath(xs, [100, 50, 80], [200, 200, 200]), "M46.0,100.0 L200.6,50.0 L355.0,80.0 L355.0,200.0 L200.6,200.0 L46.0,200.0Z");
  const input = [1, 2, 3];
  areaPath(input, [1, 1, 1], [2, 2, 2]);
  assert.deepEqual(input, [1, 2, 3]); // not reversed in place
});

test("stackBaselines: each series sits on the one below", () => {
  const [a, b, c] = stackBaselines([[1, 2, 3], [4, 5, 6], [0, 1, 0]]);
  assert.deepEqual(a, { base: [0, 0, 0], top: [1, 2, 3] });
  assert.deepEqual(b, { base: [1, 2, 3], top: [5, 7, 9] });
  assert.deepEqual(c.base, [5, 7, 9]);
  assert.deepEqual(c.top, [5, 8, 9]);
  assert.deepEqual(stackBaselines([]), []);
});

test("column width clamps between 2 and 18", () => {
  assert.equal(columnWidth(10, 100), 2); // 0.06 -> 2
  assert.equal(columnWidth(600, 10), 18); // 36 -> 18
  assert.equal(columnWidth(300, 10), 18); // 18 exactly
  assert.equal(columnWidth(200, 10), 12);
});

test("columns skip empty points and sit on the baseline", () => {
  const cols = columns([50, 100, 150], [80, 100, 40], 100, 200, 10);
  assert.equal(cols.length, 2); // the middle one has no height
  assert.deepEqual(cols[0], { x: 44, y: 80, w: 12, h: 20 });
  assert.deepEqual(cols[1], { x: 144, y: 40, w: 12, h: 60 });
});

test("markerIndexes: none under 3 series, every 2nd tick back from the last", () => {
  assert.deepEqual(markerIndexes(31, 2), []);
  const m = markerIndexes(31, 3);
  assert.ok(m.includes(30));
  assert.deepEqual(m.slice(-3), [26, 28, 30]);
  assert.deepEqual(markerIndexes(10, 4, 3), [3, 9]);
});

test("sparkPoints: zero based and min-max in the 24 high hm/spark box, flat never NaN", () => {
  const z = sparkPoints([0, 5, 10]);
  assert.equal(z.line, "0.0,22.0 50.0,12.0 100.0,2.0"); // the server's ui.SparkOf geometry
  assert.equal(z.area, "M0,24 L0.0,22.0 L50.0,12.0 L100.0,2.0 L100,24Z");
  const mm = sparkPoints([10, 15, 20], { minmax: true });
  assert.equal(mm.line, "0.0,22.0 50.0,13.5 100.0,5.0");
  assert.equal(sparkPoints([0, 5, 10], { height: 32 }).line, "0.0,30.0 50.0,16.0 100.0,2.0");
  for (const o of [{}, { minmax: true }]) {
    const flat = sparkPoints([5, 5, 5], o);
    assert.ok(!/NaN|Infinity/.test(flat.line + flat.area));
  }
  assert.deepEqual(sparkPoints([3]), { line: "", area: "" });
  assert.deepEqual(sparkPoints([]), { line: "", area: "" });
});
