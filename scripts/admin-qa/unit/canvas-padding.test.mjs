// padding drag math: value, snap, opposite side, breakpoint key, labels, and one history entry per drag.
// Run: node --test scripts/admin-qa/unit/canvas-padding.test.mjs
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
const { padValue, padWrites, padLabel, padAria, startPx, SIDES, OPPOSITE } = await import(new URL("canvas/padding.js", root));
const S = await import(new URL("builder/store.js", root));

test("padValue: top/left grow with the pointer, bottom/right against it", () => {
  assert.equal(padValue({ start: 96, d: 24, scale: 1, side: "top" }), 120);
  assert.equal(padValue({ start: 96, d: 24, scale: 1, side: "left" }), 120);
  assert.equal(padValue({ start: 96, d: 24, scale: 1, side: "bottom" }), 72);
  assert.equal(padValue({ start: 96, d: -24, scale: 1, side: "right" }), 120);
});

test("padValue: screen px are divided by the zoom", () => {
  assert.equal(padValue({ start: 0, d: 24, scale: 0.5, side: "top" }), 48);
  assert.equal(padValue({ start: 10, d: 10, scale: 2, side: "top" }), 15);
});

test("padValue: shift rounds to a multiple of 8, zero stays zero", () => {
  assert.equal(padValue({ start: 0, d: 13, scale: 1, side: "top", shift: true }), 16);
  assert.equal(padValue({ start: 0, d: 11, scale: 1, side: "top", shift: true }), 8);
  assert.equal(padValue({ start: 0, d: 0, scale: 1, side: "top", shift: true }), 0);
  assert.equal(padValue({ start: 0, d: 13, scale: 1, side: "top" }), 13);
});

test("padValue: never below 0, never above 1000, always an integer", () => {
  assert.equal(padValue({ start: 10, d: -500, scale: 1, side: "top" }), 0);
  assert.equal(padValue({ start: 10, d: 99999, scale: 1, side: "top" }), 1000);
  assert.equal(padValue({ start: 10, d: 0.4, scale: 1, side: "top" }), 10);
  assert.equal(padValue({ start: 0, d: 5, scale: 3, side: "top" }), 2);
});

test("padWrites: one op, or two with alt (the opposite side gets the same px)", () => {
  assert.deepEqual(padWrites({ id: "a", bp: "base", side: "top", px: 40 }), [{ op: "setStyle", id: "a", bp: "base", key: "pt", value: "40px" }]);
  assert.deepEqual(padWrites({ id: "a", bp: "md", side: "top", px: 40, alt: true }), [
    { op: "setStyle", id: "a", bp: "md", key: "pt", value: "40px" },
    { op: "setStyle", id: "a", bp: "md", key: "pb", value: "40px" },
  ]);
  assert.equal(padWrites({ id: "a", bp: "base", side: "left", px: 8, alt: true })[1].key, "pr");
  for (const s of Object.keys(SIDES)) assert.equal(OPPOSITE[OPPOSITE[s]], s);
});

test("labels: the inspector's wording, no suffix at base", () => {
  assert.equal(padLabel("top", "base"), "Change Padding top");
  assert.equal(padLabel("top", "md"), "Change Padding top (Tablet)");
  assert.equal(padLabel("left", "sm"), "Change Padding left (Phone)");
  assert.equal(padLabel("top", "md:hover"), "Change Padding top (Hover, Tablet)");
  assert.equal(padAria("top", 96), "Top padding, 96px");
});

test("startPx: the measured pd first, then the authored px/rem, else 0", () => {
  assert.equal(startPx([10, 20, 30, 40], "top"), 10);
  assert.equal(startPx([10, 20, 30, 40], "left", "99px"), 40);
  assert.equal(startPx(undefined, "top", "24px"), 24);
  assert.equal(startPx(undefined, "top", "1.5rem"), 24);
  assert.equal(startPx(undefined, "top", "space-4"), 0);
  assert.equal(startPx([1, 2, 3], "left", null), 0, "a short pd is not trusted");
  assert.equal(startPx(undefined, "top"), 0);
});

const doc = () => ({ v: 2, nodes: [{ id: "s1", t: "container", p: { tag: "section" }, s: { base: { pt: "96px", pb: "96px" } }, c: [] }] });
const pad = (s, bp, k) => S.find(s.doc, "s1").s?.[bp]?.[k];

test("a drag is ONE history entry: 30 moves commit nothing, pointerup commits once; undo and redo", () => {
  let s = S.createStore(doc());
  let px = 0;
  for (let i = 1; i <= 30; i++) px = padValue({ start: 96, d: i, scale: 1, side: "top", shift: i % 2 === 0 });
  assert.equal(s.past.length, 0, "moves only preview");
  s = S.commit(s, padWrites({ id: "s1", bp: "base", side: "top", px, alt: true }), { label: padLabel("top", "base") });
  assert.equal(s.past.length, 1);
  assert.equal(pad(s, "base", "pt"), `${px}px`);
  assert.equal(pad(s, "base", "pb"), `${px}px`);
  const u = S.undo(s);
  assert.equal(pad(u, "base", "pt"), "96px");
  assert.equal(pad(u, "base", "pb"), "96px", "the opposite side is restored by the same undo");
  const r = S.redo(u);
  assert.equal(pad(r, "base", "pt"), `${px}px`);
  assert.equal(pad(r, "base", "pb"), `${px}px`);
});

test("a Tablet drag writes only the md key; the Desktop value is unchanged", () => {
  let s = S.createStore(doc());
  s = S.commit(s, padWrites({ id: "s1", bp: "md", side: "top", px: 40 }), { label: padLabel("top", "md") });
  assert.equal(pad(s, "md", "pt"), "40px");
  assert.equal(pad(s, "base", "pt"), "96px");
});
