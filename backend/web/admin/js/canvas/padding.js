// Padding drag math for the canvas handles: pure (no DOM, no store). direct.js owns the gesture; this
// turns a pointer delta into a value, the value into setStyle ops, and names the history entry.
import { atLabel } from "@hm/builder/bp";

export const SIDES = { top: "pt", right: "pr", bottom: "pb", left: "pl" };
export const OPPOSITE = { top: "bottom", bottom: "top", left: "right", right: "left" };
const ORDER = ["top", "right", "bottom", "left"]; // the order of the layout node's `pd` quadruple
const MAX_PX = 1000;
const SNAP = 8;

/** The new padding in px for a drag of `d` screen px along the side's axis (y for top/bottom, x for left/right). Dragging inward grows it; shift rounds to a multiple of 8; 0..1000. */
export function padValue({ start, d, scale, side, shift }) {
  const grow = side === "top" || side === "left" ? d : -d;
  let v = start + grow / (scale || 1);
  if (shift) v = Math.round(v / SNAP) * SNAP;
  return Math.round(Math.min(MAX_PX, Math.max(0, v)));
}

/** One setStyle per side: the dragged one, plus the opposite one when alt (decided at pointerdown). */
export function padWrites({ id, bp, side, px, alt }) {
  const sides = alt ? [side, OPPOSITE[side]] : [side];
  return sides.map((s) => ({ op: "setStyle", id, bp, key: SIDES[s], value: `${px}px` }));
}

/** "Change Padding top (Tablet)"; the base key has no suffix (same wording as the inspector's). */
export const padLabel = (side, bp) => `Change Padding ${side}${bp === "base" ? "" : ` (${atLabel(bp)})`}`;

/** "Top padding, 96px": the handle's accessible name. */
export const padAria = (side, px) => `${side[0].toUpperCase()}${side.slice(1)} padding, ${px}px`;

/** The px a drag starts from: the frame's measured padding (`pd` of the layout node), else the authored value (px, or rem*16), else 0. */
export function startPx(pd, side, authored) {
  const i = ORDER.indexOf(side);
  if (Array.isArray(pd) && Number.isFinite(pd[i])) return pd[i];
  const m = /^(\d+(?:\.\d+)?)(px|rem)$/.exec(String(authored ?? ""));
  return m ? Math.round(parseFloat(m[1]) * (m[2] === "rem" ? 16 : 1)) : 0;
}
