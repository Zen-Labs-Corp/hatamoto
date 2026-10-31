// The canvas layout helpers: the zoom scale table and the breadcrumb chain.
// Run: node --test scripts/admin-qa/unit/builder-layout.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";

const { MIN_SCALE, ZOOMS, previewScale, zoomScale, crumbs } = await import(new URL("../../../backend/web/admin/js/canvas/layout.js", import.meta.url));

test("the scale never drops below the readable floor", () => {
  assert.equal(MIN_SCALE, 0.5);
  assert.equal(previewScale(100, 1280), 0.5);
  assert.equal(previewScale(2000, 1280), 1);
});

test("zoomScale: fit follows the pane, fixed zooms do not, none goes below the floor", () => {
  assert.equal(zoomScale("fit", 700, 1280), previewScale(700, 1280));
  assert.equal(zoomScale("fit", 0, 1280), 1, "no pane yet");
  for (const [mode, want] of [[100, 1], [75, 0.75], [50, 0.5]]) {
    assert.equal(zoomScale(mode, 300, 1280), want, `${mode}% ignores the pane`);
    assert.equal(zoomScale(mode, 3000, 390), want);
  }
  assert.equal(zoomScale(10, 700, 1280), previewScale(700, 1280), "an unknown mode falls back to fit");
  assert.deepEqual(Object.keys(ZOOMS).sort(), ["100", "50", "75", "fit"]);
});

test("crumbs: the ancestor chain root first; v1-shaped and unknown ids", () => {
  const nodes = [
    { id: "sec", k: "s", t: "section", p: "" },
    { id: "cols", k: "n", t: "columns", p: "sec" },
    { id: "col1", k: "n", t: "container", p: "cols" },
    { id: "h1", k: "n", t: "heading", p: "col1" },
    { id: "about", k: "s", t: "component", p: "" }, // a v1-shaped section: top level, no parent
  ];
  assert.deepEqual(crumbs(nodes, "h1").map((c) => c.id), ["sec", "cols", "col1", "h1"]);
  assert.deepEqual(crumbs(nodes, "sec").map((c) => c.id), ["sec"]);
  assert.deepEqual(crumbs(nodes, "about"), [{ id: "about", k: "s", t: "component" }]);
  assert.deepEqual(crumbs(nodes, "nope"), []);
  assert.deepEqual(crumbs([{ id: "a", k: "n", t: "x", p: "b" }, { id: "b", k: "n", t: "x", p: "a" }], "a").length, 2, "a cycle terminates");
});

const { placeToolbar } = await import(new URL("../../../backend/web/admin/js/canvas/layout.js", import.meta.url));
const view = { top: 0, left: 0, right: 800, bottom: 600 };
const bar = { w: 300, h: 32 };

test("placeToolbar: above the element, off its first line", () => {
  const r = placeToolbar({ box: { x: 40, y: 200, w: 400, h: 80 }, bar, view });
  assert.deepEqual(r, { x: 40, y: 160, flip: false, inside: false });
});

test("placeToolbar: flips below at the top edge of the view", () => {
  const r = placeToolbar({ box: { x: 40, y: 10, w: 400, h: 80 }, bar, view });
  assert.deepEqual(r, { x: 40, y: 98, flip: true, inside: false });
});

test("placeToolbar: clamps into the view on both sides", () => {
  assert.equal(placeToolbar({ box: { x: 700, y: 200, w: 90, h: 40 }, bar, view }).x, 500, "right edge");
  assert.equal(placeToolbar({ box: { x: -50, y: 200, w: 90, h: 40 }, bar, view }).x, 0, "left edge");
});

test("placeToolbar: `below` (text editing) puts it under the element, back above when there is no room under", () => {
  assert.equal(placeToolbar({ box: { x: 0, y: 200, w: 100, h: 40 }, bar, view, below: true }).y, 248);
  const r = placeToolbar({ box: { x: 0, y: 400, w: 100, h: 190 }, bar, view, below: true });
  assert.deepEqual([r.y, r.flip], [360, false]);
});

test("placeToolbar: no room above or below sits inside the element's top edge, 8px in", () => {
  assert.deepEqual(placeToolbar({ box: { x: 0, y: 0, w: 100, h: 600 }, bar, view }), { x: 8, y: 8, flip: false, inside: true }, "a section at the page top");
  assert.deepEqual(placeToolbar({ box: { x: 0, y: 5, w: 100, h: 590 }, bar, view }), { x: 8, y: 13, flip: false, inside: true });
  const r = placeToolbar({ box: { x: 0, y: -300, w: 100, h: 1200 }, bar, view });
  assert.deepEqual([r.y, r.inside], [0, true], "a tall element scrolled past: clamped to the top of the view");
});

const { insertionPoints } = await import(new URL("../../../backend/web/admin/js/canvas/points.js", import.meta.url));

test("insertionPoints: the page level has a point between and around the top-level nodes", () => {
  const nodes = [
    { id: "a", k: "n", t: "container", p: "", x: 0, y: 0, w: 1000, h: 100, c: 1, dir: "column" },
    { id: "b", k: "n", t: "container", p: "", x: 0, y: 120, w: 1000, h: 80, c: 1, dir: "column" },
  ];
  const pts = insertionPoints(nodes, new Set([""])).filter((p) => p.parent === "");
  assert.deepEqual(pts.map((p) => [p.index, p.after, p.before, p.y]), [[0, null, "a", 0], [1, "a", "b", 110], [2, "b", null, 200]]);
  assert.ok(pts.every((p) => p.dir === "column" && p.x === 500 && p.len === 1000));
});

test("insertionPoints: nested row children get vertical lines, edge points sit at the container edge (no inset into a child), a container is listed only when in scope", () => {
  const nodes = [
    { id: "s", k: "n", t: "columns", p: "", x: 0, y: 0, w: 900, h: 200, c: 1, dir: "row" },
    { id: "c1", k: "n", t: "container", p: "s", x: 0, y: 0, w: 400, h: 200, c: 1, dir: "column" },
    { id: "c2", k: "n", t: "container", p: "s", x: 500, y: 0, w: 400, h: 200, c: 1, dir: "column" },
    { id: "h", k: "n", t: "heading", p: "c1", x: 0, y: 0, w: 400, h: 40 },
    { id: "t", k: "n", t: "text", p: "c2", x: 500, y: 0, w: 400, h: 40 },
  ];
  assert.deepEqual(insertionPoints(nodes, new Set([""])).filter((p) => p.parent === "s"), [], "out of scope: nothing inside s");
  const inS = insertionPoints(nodes, new Set(["s"])).filter((p) => p.parent === "s");
  assert.deepEqual(inS.map((p) => [p.index, p.x, p.dir, p.len]), [[0, 0, "row", 200], [1, 450, "row", 200], [2, 900, "row", 200]]);
  assert.deepEqual(inS.map((p) => p.key), ["s:0", "s:1", "s:2"]);
});

test("insertionPoints: an empty container always has its own point; a grid lays out along the row", () => {
  const nodes = [
    { id: "g", k: "n", t: "container", p: "", x: 0, y: 0, w: 600, h: 300, c: 1, dir: "grid" },
    { id: "e", k: "n", t: "container", p: "g", x: 0, y: 0, w: 300, h: 64, c: 1, dir: "column" },
    { id: "f", k: "n", t: "container", p: "g", x: 300, y: 0, w: 300, h: 64, c: 1, dir: "column" },
  ];
  const pts = insertionPoints(nodes, new Set());
  assert.deepEqual(pts.map((p) => [p.parent, p.index, p.empty, p.x, p.y]), [["e", 0, true, 150, 32], ["f", 0, true, 450, 32]]);
  assert.equal(insertionPoints(nodes, new Set(["g"])).filter((p) => p.parent === "g" && !p.empty).every((p) => p.dir === "row"), true);
  assert.deepEqual(insertionPoints([], new Set([""])), []);
});

test("insertionPoints: wrapped rows get a point between two items only on the same line, else at the line end on that line", () => {
  // a 3-column grid of 6 items, two lines of 100 px with a 20 px gap
  const item = (id, i) => ({ id, k: "n", t: "container", p: "g", x: (i % 3) * 320, y: Math.floor(i / 3) * 120, w: 300, h: 100 });
  const nodes = [{ id: "g", k: "n", t: "container", p: "", x: 0, y: 0, w: 940, h: 220, c: 1, dir: "grid" }, ...[0, 1, 2, 3, 4, 5].map((i) => item("i" + i, i))];
  const pts = insertionPoints(nodes, new Set(["g"])).filter((p) => p.parent === "g");
  const at = (i) => pts.find((p) => p.index === i);
  assert.deepEqual([at(1).x, at(1).y, at(1).len], [310, 50, 100], "between item 0 and 1: in their gap, on their line");
  assert.deepEqual([at(3).x, at(3).y, at(3).len], [940, 50, 100], "item 2 ends line 1, item 3 starts line 2: at the end of line 1, not the middle of the grid");
  assert.deepEqual([at(4).x, at(4).y], [310, 170]);
  assert.ok(pts.every((p) => p.len <= 100), "no line spans the grid");
  assert.ok(pts.every((p) => !nodes.slice(1).some((n) => p.x > n.x + 1 && p.x < n.x + n.w - 1 && p.y > n.y && p.y < n.y + n.h) || p.index === 0), "none sits inside an item");
});

test("insertionPoints: points closer than gap are dropped, the selected scope's first", () => {
  const nodes = [
    { id: "a", k: "n", t: "container", p: "", x: 0, y: 0, w: 1000, h: 100, c: 1, dir: "column" },
    { id: "b", k: "n", t: "container", p: "", x: 0, y: 100, w: 1000, h: 100, c: 1, dir: "column" },
    { id: "h", k: "n", t: "heading", p: "b", x: 0, y: 100, w: 1000, h: 40 },
    { id: "t", k: "n", t: "text", p: "b", x: 0, y: 140, w: 1000, h: 40 },
  ];
  const all = insertionPoints(nodes, new Set(["", "b"]), { gap: 0 });
  const few = insertionPoints(nodes, new Set(["", "b"]), { gap: 30 });
  assert.ok(few.length < all.length);
  for (const p of few) for (const q of few) if (p !== q) assert.ok(Math.hypot(p.x - q.x, p.y - q.y) >= 30, `${p.key} ${q.key}`);
  assert.ok(few.some((p) => p.key === ":1"), "the page-level point (first in scope) stays over the nested one at the same place");
});

test("placeToolbar: the inside toolbar never covers `avoid` (the top padding handle)", () => {
  const box = { x: 0, y: 0, w: 600, h: 600 };
  const avoid = { x: 278, y: 0, w: 44, h: 24 }; // top handle, 0 padding, centred
  const wide = { w: 400, h: 32 };
  const r = placeToolbar({ box, bar: wide, view, avoid });
  assert.ok(r.inside);
  assert.ok(r.y >= avoid.y + avoid.h, `toolbar top ${r.y} is under the handle's bottom ${avoid.y + avoid.h}`);
  assert.equal(placeToolbar({ box, bar: { w: 100, h: 32 }, view, avoid }).y, 8, "no overlap sideways: stays 8px in");
  assert.equal(placeToolbar({ box: { x: 0, y: 200, w: 600, h: 100 }, bar: wide, view, avoid }).inside, false, "only the inside placement moves");
});
