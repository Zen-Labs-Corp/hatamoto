// Scrub, typed-value and preset helpers of the compact box model.
// Run: node --test scripts/admin-qa/unit/builder-boxmodel-scrub.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);

const { scrubValue, axisOf, dragDelta, parseTyped, displayOf, presetSteps, attachScrub } = await import(new URL("builder/scrub.js", root));
const table = JSON.parse(readFileSync(new URL("../../../backend/testdata/stylespec/table.json", import.meta.url), "utf8"));
const entry = (k) => table.find((e) => e.key === k);

// A bare element: listeners by event name, focus/select recorders.
const el = () => {
  const on = {};
  const e = { on, calls: [], addEventListener: (t, f) => (on[t] ??= []).push(f), focus: () => e.calls.push("focus"), select: () => e.calls.push("select"), setPointerCapture: (id) => e.calls.push("capture:" + id) };
  e.fire = (t, ev = {}) => (on[t] ?? []).forEach((f) => f({ pointerId: 1, clientX: 0, clientY: 0, preventDefault() {}, ...ev }));
  return e;
};

test("vertical drag: up grows every side, down shrinks", () => {
  assert.equal(axisOf("top"), "v");
  assert.equal(axisOf("bottom"), "v");
  assert.equal(axisOf("left"), "h");
  assert.equal(dragDelta("v", { dx: 5, dy: -7 }), 7);
  assert.equal(dragDelta("v", { dx: 0, dy: 4 }), -4);
});

test("horizontal drag: right grows, left shrinks", () => {
  assert.equal(dragDelta("h", { dx: 9, dy: 50 }), 9);
  assert.equal(dragDelta("h", { dx: -3, dy: 0 }), -3);
});

test("scrubValue: 1 unit per px, Shift x10, clamped to the bounds, negative allowed when the bounds allow", () => {
  assert.equal(scrubValue({ start: 10, dpx: 5 }), 15);
  assert.equal(scrubValue({ start: 10, dpx: 5, shift: true }), 60);
  assert.equal(scrubValue({ start: 10, dpx: -50, bounds: { min: 0, max: 200 } }), 0, "padding stops at 0");
  assert.equal(scrubValue({ start: 10, dpx: -50, bounds: { min: -200, max: 200 } }), -40, "margin goes negative");
  assert.equal(scrubValue({ start: 190, dpx: 50, bounds: { min: 0, max: 200 } }), 200);
  assert.equal(scrubValue({ start: 1, dpx: 3, step: 0.1 }), 1.3, "no float noise");
});

test("parseTyped: bare numbers take the unit, units are kept, 2rem works", () => {
  const pt = entry("pt");
  assert.deepEqual(parseTyped(pt, "48"), { ok: true, value: "48px" });
  assert.deepEqual(parseTyped(pt, "0"), { ok: true, value: "0" });
  assert.deepEqual(parseTyped(pt, " 2rem "), { ok: true, value: "2rem" });
  assert.deepEqual(parseTyped(pt, "1.5EM"), { ok: true, value: "1.5em" });
  assert.deepEqual(parseTyped(pt, "space-4"), { ok: true, value: "space-4" });
  assert.deepEqual(parseTyped(pt, ""), { ok: true, value: null });
});

test("parseTyped: auto only where the entry takes it, garbage is refused", () => {
  assert.deepEqual(parseTyped(entry("mt"), "auto"), { ok: true, value: "auto" });
  assert.deepEqual(parseTyped(entry("pt"), "auto"), { ok: false });
  assert.deepEqual(parseTyped(entry("pt"), "abc"), { ok: false });
  assert.deepEqual(parseTyped(entry("pt"), "12 px"), { ok: false });
  assert.deepEqual(parseTyped(entry("pt"), "--4"), { ok: false });
});

test("displayOf: px hidden, other units shown, tokens resolved, empty muted", () => {
  const palette = { space: { 4: "1rem", 7: "32px" } };
  assert.deepEqual(displayOf("96px"), { text: "96", unit: "", muted: false });
  assert.deepEqual(displayOf("2rem"), { text: "2", unit: "rem", muted: false });
  assert.deepEqual(displayOf("space-4", palette), { text: "1", unit: "rem", muted: false });
  assert.deepEqual(displayOf("space-7", palette), { text: "32", unit: "", muted: false });
  assert.deepEqual(displayOf("auto"), { text: "auto", unit: "", muted: false });
  assert.deepEqual(displayOf(null), { text: "0", unit: "", muted: true });
  assert.deepEqual(displayOf("space-9", palette), { text: "space-9", unit: "", muted: false }, "an unresolved token shows its name");
});

test("presetSteps: theme tokens when the scale has them, px otherwise", () => {
  const full = presetSteps({ space: Object.fromEntries(Array.from({ length: 13 }, (_, i) => [i, "1rem"])) });
  assert.deepEqual(full.map((p) => [p.label, p.value]), [["0", "0"], ["S", "space-4"], ["M", "space-7"], ["L", "space-10"], ["XL", "space-12"]]);
  const none = presetSteps({ colors: [], space: {} });
  assert.deepEqual(none.map((p) => p.value), ["0", "32px", "64px", "96px", "128px"]);
  assert.deepEqual(presetSteps(null).map((p) => p.label), ["0", "S", "M", "L", "XL"]);
  const partial = presetSteps({ space: { 4: "1rem" } });
  assert.deepEqual(partial.map((p) => p.value), ["0", "space-4", "64px", "96px", "128px"]);
});

test("attachScrub: a press under 3px is a click, it focuses and selects, nothing is applied", () => {
  const e = el();
  const writes = [];
  attachScrub(e, { axis: "v", get: () => 10, apply: (v, o) => writes.push([v, o]) });
  e.fire("pointerdown", { clientX: 5, clientY: 5 });
  e.fire("pointermove", { clientX: 6, clientY: 6 });
  e.fire("pointerup");
  assert.deepEqual(writes, []);
  assert.deepEqual(e.calls, ["capture:1", "focus", "select"]);
});

test("attachScrub: a vertical drag applies values with ONE coalesce key per gesture, a second gesture gets another", () => {
  const e = el();
  const writes = [];
  const bracket = [];
  attachScrub(e, { axis: "v", get: () => 10, apply: (v, o) => writes.push([v, o.coalesce]), onStart: () => bracket.push("start"), onEnd: () => bracket.push("end") });
  e.fire("pointerdown", { clientX: 0, clientY: 100 });
  e.fire("pointermove", { clientX: 0, clientY: 90 }); // up 10
  e.fire("pointermove", { clientX: 0, clientY: 85, shiftKey: true }); // up 15, x10
  e.fire("pointerup");
  assert.deepEqual(writes.map((w) => w[0]), [20, 160]);
  assert.equal(new Set(writes.map((w) => w[1])).size, 1, "one key");
  assert.match(writes[0][1], /^bm:\d+$/);
  assert.deepEqual(bracket, ["start", "end"]);
  assert.equal(e.calls.includes("focus"), false, "a drag does not focus");
  e.fire("pointerdown", { clientX: 0, clientY: 0 });
  e.fire("pointermove", { clientX: 0, clientY: -20 });
  e.fire("pointerup");
  assert.notEqual(writes[2][1], writes[0][1]);
});

test("attachScrub: horizontal drag follows dx, bounds clamp, a session prefix names the key", () => {
  const e = el();
  const writes = [];
  attachScrub(e, { axis: "h", get: () => 4, apply: (v, o) => writes.push([v, o.coalesce]), bounds: { min: 0, max: 50 }, session: "ls:w1" });
  e.fire("pointerdown", { clientX: 0, clientY: 0 });
  e.fire("pointermove", { clientX: -30, clientY: 0 });
  e.fire("pointermove", { clientX: 200, clientY: 0 });
  e.fire("pointerup");
  assert.deepEqual(writes.map((w) => w[0]), [0, 50]);
  assert.match(writes[0][1], /^ls:w1:\d+$/);
});

test("attachScrub: pointercancel and blur close the gesture; later moves do nothing", () => {
  for (const closer of ["pointercancel", "blur"]) {
    const e = el();
    const writes = [];
    let ended = 0;
    attachScrub(e, { axis: "v", get: () => 0, apply: (v) => writes.push(v), onEnd: () => ended++ });
    e.fire("pointerdown", { clientX: 0, clientY: 10 });
    e.fire("pointermove", { clientX: 0, clientY: 0 });
    e.fire(closer);
    e.fire("pointermove", { clientX: 0, clientY: -50 });
    assert.deepEqual(writes, [10], closer);
    assert.equal(ended, 1, closer);
  }
});

test("attachScrub: the move is rAF-throttled, the last value is flushed on pointerup", () => {
  const frames = [];
  globalThis.requestAnimationFrame = (f) => frames.push(f);
  try {
    const e = el();
    const writes = [];
    attachScrub(e, { axis: "v", get: () => 0, apply: (v) => writes.push(v) });
    e.fire("pointerdown", { clientX: 0, clientY: 100 });
    e.fire("pointermove", { clientX: 0, clientY: 90 });
    e.fire("pointermove", { clientX: 0, clientY: 80 });
    assert.equal(frames.length, 1, "one frame for two moves");
    assert.deepEqual(writes, []);
    frames.shift()();
    assert.deepEqual(writes, [20]);
    e.fire("pointermove", { clientX: 0, clientY: 70 });
    e.fire("pointerup");
    assert.deepEqual(writes, [20, 30], "pointerup flushes the pending value");
    frames.shift()();
    assert.deepEqual(writes, [20, 30], "the late frame adds nothing");
  } finally {
    delete globalThis.requestAnimationFrame;
  }
});

// A gesture the browser never finished must not keep scrubbing on later moves.
const live = (extra = {}) => {
  const e = el();
  const writes = [];
  let ended = 0;
  attachScrub(e, { axis: "h", get: () => 10, apply: (v, o) => writes.push([v, o.coalesce]), onEnd: () => ended++, ...extra });
  return { e, writes, ended: () => ended };
};

test("attachScrub: only the primary button starts a gesture", () => {
  const { e, writes } = live();
  e.fire("pointerdown", { button: 2, clientX: 0 });
  e.fire("pointermove", { clientX: 30 });
  e.fire("pointerdown", { button: 0, isPrimary: false, clientX: 0 });
  e.fire("pointermove", { clientX: 30 });
  assert.deepEqual(writes, []);
  assert.deepEqual(e.calls, [], "no capture, no focus");
});

test("attachScrub: a move with no button held ends the gesture once and writes nothing more", () => {
  const { e, writes, ended } = live();
  e.fire("pointerdown", { clientX: 0 });
  e.fire("pointermove", { clientX: 5, buttons: 1 });
  e.fire("pointermove", { clientX: 9, buttons: 0 }); // the pointerup was swallowed (context menu)
  e.fire("pointermove", { clientX: 40, buttons: 0 });
  assert.deepEqual(writes.map((w) => w[0]), [15], "committed once, at the last held position");
  assert.equal(ended(), 1);
});

test("attachScrub: lostpointercapture ends the gesture", () => {
  const { e, writes, ended } = live();
  e.fire("pointerdown", { clientX: 0 });
  e.fire("pointermove", { clientX: 5 });
  e.fire("lostpointercapture");
  e.fire("pointermove", { clientX: 50 });
  assert.deepEqual(writes.map((w) => w[0]), [15]);
  assert.equal(ended(), 1);
});

test("attachScrub: no gesture while the field itself has focus (being typed in)", () => {
  const { e, writes } = live();
  globalThis.document = { activeElement: e };
  try {
    e.fire("pointerdown", { clientX: 0 });
    e.fire("pointermove", { clientX: 30 });
    assert.deepEqual(writes, []);
    assert.deepEqual(e.calls, [], "the native caret is left alone");
  } finally {
    delete globalThis.document;
  }
});
