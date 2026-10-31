// Scrub, typed-value and preset helpers for the compact box model and the scrubbable field labels: pure logic plus one
// small pointer attach, node-tested (scripts/admin-qa/unit/builder-boxmodel-scrub.test.mjs). A lazy chunk: nothing on
// the first-paint path imports it. Run: node --test scripts/admin-qa/unit/builder-boxmodel-scrub.test.mjs

import { probe, tidy } from "@hm/builder/controls";

/** Pointer travel (px) under which a press is a click, not a drag. */
const CLICK_SLOP = 3;

/** start moved by dpx pixels (times step), times 10 with Shift, rounded and kept inside bounds ({min, max}) when given. */
export function scrubValue({ start, dpx, shift = false, bounds = null, step = 1 }) {
  const next = tidy(start + dpx * (shift ? 10 : 1) * step);
  return bounds ? Math.min(bounds.max, Math.max(bounds.min, next)) : next;
}

/** "v" for the top and bottom sides (dragged vertically), "h" for left and right. */
export const axisOf = (side) => (side === "top" || side === "bottom" ? "v" : "h");

/** The pointer travel that grows a value: up is bigger on a vertical axis, right is bigger on a horizontal one. */
export const dragDelta = (axis, { dx, dy }) => (axis === "v" ? -dy : dx);

const TYPED = /^(-?\d+(?:\.\d+)?)([a-z%]*)$/i;

/**
 * What a typed string means for entry: {ok: true, value} (value null clears), or {ok: false}. A bare number gets unit,
 * "auto" and the entry's token names pass through. The caller still runs the table's own check on value.
 */
export function parseTyped(entry, text, unit = "px") {
  const t = String(text ?? "").trim();
  if (t === "") return { ok: true, value: null };
  if (Object.hasOwn(entry?.tokens ?? {}, t)) return { ok: true, value: t };
  if (t.toLowerCase() === "auto") return probe(entry).auto ? { ok: true, value: "auto" } : { ok: false };
  const m = TYPED.exec(t);
  if (!m) return { ok: false };
  const u = m[2].toLowerCase();
  if (u === "") return { ok: true, value: Number(m[1]) === 0 ? "0" : `${m[1]}${unit}` };
  return { ok: true, value: `${m[1]}${u}` };
}

/**
 * How a stored value shows in a box model number: {text, unit, muted}. px is not shown, any other unit is; a space token
 * shows what it resolves to through palette.space; "auto" shows as is; an empty value is a muted "0".
 */
export function displayOf(value, palette = null) {
  if (value === null || value === undefined || value === "") return { text: "0", unit: "", muted: true };
  let v = String(value);
  if (/^space-\d+$/.test(v)) v = palette?.space?.[v.slice(6)] ?? v;
  const m = TYPED.exec(v);
  if (!m) return { text: v, unit: "", muted: false };
  return { text: m[1], unit: m[2] === "px" ? "" : m[2], muted: false };
}

const FALLBACK_PX = ["0", "32px", "64px", "96px", "128px"];
const PRESET_TOKENS = ["space-0", "space-4", "space-7", "space-10", "space-12"];
const PRESET_LABELS = ["0", "S", "M", "L", "XL"];

/** The 0 / S / M / L / XL presets: the theme's spacing token when the scale has it, else a px value. */
export const presetSteps = (palette) =>
  PRESET_LABELS.map((label, i) => {
    const token = PRESET_TOKENS[i];
    return { label, value: i === 0 ? "0" : palette?.space && Object.hasOwn(palette.space, token.slice(6)) ? token : FALLBACK_PX[i] };
  });

let sessions = 0;

/**
 * Makes el a scrub handle. A press that moves less than 3 px is a click (the element is focused and selected); a drag
 * calls apply(value, {coalesce: key}) once per animation frame with ONE key for the whole gesture, so the store merges it
 * into one undo step, then closes the gesture on pointerup, pointercancel, lostpointercapture, blur or a move with no button held.
 * Only the primary button starts one, and not while el itself has focus (it is being typed in).
 * axis "v" | "h"; get() -> the number the drag starts from (null: do not scrub); apply(n, opts); session: key prefix ("bm", "ls:<id>");
 * step per pixel and bounds {min, max} (each a value or a function read when the drag starts); sign -1 flips the direction; onStart / onEnd bracket a drag. Returns a detach function.
 */
export function attachScrub(el, { axis, get, apply, session = "bm", step = 1, bounds = null, sign = 1, onStart, onEnd }) {
  const frame = globalThis.requestAnimationFrame ?? ((f) => f());
  let g = null;
  let queued = false;
  const flush = () => {
    queued = false;
    if (g && g.pending !== null) {
      const v = g.pending;
      g.pending = null;
      apply(v, { coalesce: g.key });
    }
  };
  const end = () => {
    if (!g) return;
    flush();
    const done = g;
    g = null;
    if (!done.dragging) {
      el.focus?.();
      el.select?.();
    } else onEnd?.();
  };
  const down = (e) => {
    if ((e.button ?? 0) !== 0 || e.isPrimary === false) return; // a right-click or a second finger starts nothing
    if ((el.ownerDocument ?? globalThis.document)?.activeElement === el) return; // being typed in: the native caret and selection win
    const start = get();
    if (start === null) return; // nothing to scrub from (auto, a custom value): the press is left to the browser
    g = { x: e.clientX, y: e.clientY, start: Number(start) || 0, step: typeof step === "function" ? step() : step, bounds: typeof bounds === "function" ? bounds() : bounds, key: `${session}:${++sessions}`, dragging: false, pending: null };
    el.setPointerCapture?.(e.pointerId);
    e.preventDefault?.();
  };
  const move = (e) => {
    if (!g) return;
    if (e.buttons === 0) return end(); // the pointerup was lost (context menu, alt-tab): the drag is over
    const d = { dx: e.clientX - g.x, dy: e.clientY - g.y };
    if (!g.dragging) {
      if (Math.max(Math.abs(d.dx), Math.abs(d.dy)) < CLICK_SLOP) return;
      g.dragging = true;
      onStart?.();
    }
    g.pending = scrubValue({ start: g.start, dpx: sign * dragDelta(axis, d), shift: !!e.shiftKey, bounds: g.bounds, step: g.step });
    if (!queued) {
      queued = true;
      frame(flush);
    }
  };
  el.addEventListener("pointerdown", down);
  el.addEventListener("pointermove", move);
  el.addEventListener("pointerup", end);
  el.addEventListener("pointercancel", end);
  el.addEventListener("lostpointercapture", end);
  el.addEventListener("blur", end);
  return () => {
    el.removeEventListener?.("pointerdown", down);
    el.removeEventListener?.("pointermove", move);
    el.removeEventListener?.("pointerup", end);
    el.removeEventListener?.("pointercancel", end);
    el.removeEventListener?.("lostpointercapture", end);
    el.removeEventListener?.("blur", end);
  };
}
