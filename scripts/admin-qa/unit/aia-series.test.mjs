// AI analytics series order and markers. Run: node --test scripts/admin-qa/unit/aia-series.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const src = new URL("../../../backend/web/admin/js/aia/series.js", import.meta.url);
const { SERIES, seriesFor, vizClass, markerPath } = await import(src);

test("imports in Node with no document or window", () => {
  assert.equal(typeof document, "undefined");
  assert.equal(typeof window, "undefined");
});

test("series follow the prototype order, violet never beside fuchsia", () => {
  assert.deepEqual(SERIES.map((s) => s.n), [1, 2, 3, 5, 4, 6]);
  const n = SERIES.map((s) => s.n);
  assert.notEqual(Math.abs(n.indexOf(3) - n.indexOf(4)), 1);
});

test("other is always the last slot, the rest never use it", () => {
  assert.equal(seriesFor(0, true).n, 6);
  assert.equal(seriesFor(3, true).n, 6);
  assert.equal(seriesFor(5, false).n, 1); // wraps over the five named slots
  assert.deepEqual([0, 1, 2, 3, 4].map((i) => seriesFor(i, false).n), [1, 2, 3, 5, 4]);
  assert.equal(vizClass(SERIES[3]), "hm-viz--series-5");
});

test("dashes differ per series so colour is not the only cue", () => {
  assert.equal(new Set(SERIES.map((s) => s.dash)).size, SERIES.length);
});

test("every marker kind returns finite geometry and no colour", () => {
  for (const kind of ["circle", "square", "triangle", "cross", "diamond"]) {
    const { tag, attrs } = markerPath(kind, 10.123, 20.987, 4);
    assert.ok(tag);
    const text = Object.values(attrs).join(" ");
    assert.ok(!/NaN|undefined|#|rgb/.test(text), kind + ": " + text);
    assert.match(attrs.class, /hm-aia-marker/);
  }
  assert.deepEqual(markerPath("circle", 10.123, 20.987, 3), { tag: "circle", attrs: { class: "hm-aia-marker", cx: 10.1, cy: 21, r: 3, "stroke-width": 1 } });
  assert.equal(markerPath("square", 10, 10, 3).attrs.width, 6);
  assert.equal(markerPath("triangle", 10, 10, 3).attrs.points, "10,6 14,13 6,13");
  assert.equal(markerPath("diamond", 10, 10, 3).attrs.points, "10,6 14,10 10,14 6,10");
  assert.equal(markerPath("cross", 10, 10, 3).attrs.d, "M7 7L13 13M13 7L7 13");
  assert.match(markerPath("cross", 0, 0, 3).attrs.class, /--cross/);
});
