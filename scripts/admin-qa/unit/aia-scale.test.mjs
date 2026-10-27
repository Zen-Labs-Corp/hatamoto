// AI analytics chart scales and layout. Run: node --test scripts/admin-qa/unit/aia-scale.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const src = new URL("../../../backend/web/admin/js/aia/scale.js", import.meta.url);
const { GEOM, nice, tickCount, yTicks, xTickStep, xTickIndexes, heightFor, layout, plotWidth, linear } = await import(src);

test("imports in Node with no document or window", () => {
  assert.equal(typeof document, "undefined");
  assert.equal(typeof window, "undefined");
});

// Expected values were produced by running the prototype's nice() (ai-analytics.html L642) in node.
test("nice matches the prototype", () => {
  assert.deepEqual(nice(0, true), { max: 4, step: 1 });
  assert.deepEqual(nice(0, false), { max: 1, step: 0.25 });
  assert.deepEqual(nice(1, true), { step: 1, max: 1 });
  assert.deepEqual(nice(3, true), { step: 1, max: 3 });
  assert.deepEqual(nice(7, true), { step: 2, max: 8 });
  assert.deepEqual(nice(10, true), { step: 5, max: 10 });
  assert.deepEqual(nice(1234), { step: 500, max: 1500 });
  assert.deepEqual(nice(0.8), { step: 0.2, max: 0.8 });
  assert.deepEqual(nice(0.045), { step: 0.02, max: 0.06 });
  assert.deepEqual(nice(95, true, 2), { step: 50, max: 100 });
  assert.deepEqual(nice(55574, true), { step: 20000, max: 60000 });
});

test("integer axes never step under 1", () => {
  assert.equal(nice(2, true).step, 1);
  assert.ok(nice(2, false).step < 1);
});

test("ticks: short panels get two gridlines", () => {
  assert.equal(tickCount(109), 2);
  assert.equal(tickCount(110), 4);
  assert.deepEqual(yTicks(1500, 500), [0, 500, 1000, 1500]);
  assert.deepEqual(yTicks(1, 0.25), [0, 0.25, 0.5, 0.75, 1]);
});

test("x ticks are anchored to the last point", () => {
  assert.equal(xTickStep(31, 620), 4); // floor(620/62)=10 labels -> ceil(31/10)
  assert.equal(xTickStep(3, 10), 3); // fewer than one label of room still gives a step
  assert.equal(xTickStep(1, 600), 1);
  const idx = xTickIndexes(31, 3);
  assert.equal(idx.at(-1), 30);
  assert.equal(idx[0], 0); // (30 - 0) % 3 === 0
  assert.deepEqual(xTickIndexes(7, 3), [0, 3, 6]);
  assert.deepEqual(xTickIndexes(8, 3), [1, 4, 7]);
  assert.deepEqual(xTickIndexes(1, 1), [0]);
});

test("heights follow the prototype table", () => {
  assert.equal(heightFor({ hero: true, panels: 1, phone: false }), 320);
  assert.equal(heightFor({ hero: false, panels: 1, phone: false }), 240);
  assert.equal(heightFor({ hero: true, panels: 1, phone: true }), 220);
  assert.equal(heightFor({ hero: false, panels: 1, phone: true }), 190);
  assert.equal(heightFor({ hero: true, panels: 2, phone: true }), 290);
  assert.equal(heightFor({ hero: true, panels: 2, phone: false }), 320);
});

test("layout: single panel fills between the top and bottom pads", () => {
  const [p] = layout({ height: 240, panels: 1 });
  assert.equal(p.top, GEOM.TOP);
  assert.equal(p.bottom, 240 - GEOM.BOT);
  assert.equal(p.innerH, 240 - GEOM.TOP - GEOM.BOT);
});

test("layout: panels never overlap and fill the height", () => {
  for (const height of [290, 320]) {
    const ps = layout({ height, panels: 2 });
    assert.equal(ps.length, 2);
    assert.ok(ps[0].bottom + GEOM.GAP + GEOM.PLABEL <= ps[1].top + 1e-9);
    assert.equal(ps[0].top, GEOM.TOP + GEOM.PLABEL);
    assert.ok(Math.abs(ps[1].bottom - (height - GEOM.BOT)) < 1e-9);
    assert.ok(ps[0].innerH > 0 && ps[0].innerH === ps[1].innerH);
  }
  assert.equal(layout({ height: 290, panels: 2 })[0].innerH, (290 - 8 - 26 - 24 - 36) / 2);
});

test("plotWidth and linear", () => {
  assert.equal(plotWidth(700), 700 - 46 - 22);
  const y = linear(0, 10, 100, 0);
  assert.equal(y(0), 100);
  assert.equal(y(10), 0);
  assert.ok(Number.isFinite(linear(5, 5, 0, 1)(5)));
});
