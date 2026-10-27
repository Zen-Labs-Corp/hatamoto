// Axis scales, tick steps and panel layout for the AI analytics time chart,
// ported from the prototype's timeChart. No DOM, so Node can import it.

// Left axis gutter, right pad, top pad, bottom (x labels), gap between
// stacked panels, and the label row above each panel when there are several.
export const GEOM = { L: 46, RT: 22, TOP: 8, BOT: 26, GAP: 24, PLABEL: 18 };

/** Nice axis maximum and step. Integer axes never use a step under 1. */
export function nice(max, int, ticks = 4) {
  if (max <= 0) return { max: int ? ticks : 1, step: int ? 1 : 1 / ticks };
  const raw = max / ticks;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const f = raw / mag;
  let step = (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * mag;
  if (int) step = Math.max(1, step);
  return { step, max: step * Math.ceil(max / step) };
}

/** Short panels get two gridlines instead of four. */
export const tickCount = (panelHeight) => (panelHeight < 110 ? 2 : 4);

/** [0, step, 2*step, ...] up to and including max. */
export function yTicks(max, step) {
  const out = [];
  for (let t = 0; t <= max + 1e-9; t += step) out.push(t);
  return out;
}

/** Spacing of x labels: one per 62px of plot width at most. */
export const xTickStep = (n, innerWidth) => Math.max(1, Math.ceil(n / Math.max(1, Math.floor(innerWidth / 62))));

/** Indexes that get an x label, anchored to the LAST point, ascending. */
export function xTickIndexes(n, step) {
  const out = [];
  for (let i = n - 1; i >= 0; i -= step) out.unshift(i);
  return out;
}

/** Chart height in px. Phone is the viewport query (max-width: 599px). */
export function heightFor({ hero, panels = 1, phone }) {
  if (phone) return hero ? (panels > 1 ? 290 : 220) : 190;
  return hero ? 320 : 240;
}

export const plotWidth = (width) => width - GEOM.L - GEOM.RT;

/**
 * Vertical placement of `panels` stacked panels in `height` px. Each entry is
 * {top, bottom, innerH}: the plot area of that panel. Several panels get a
 * label row above each (`labelled`, default when panels > 1).
 */
export function layout({ height, panels, labelled = panels > 1 }) {
  const lab = labelled ? GEOM.PLABEL : 0;
  const innerH = (height - GEOM.TOP - GEOM.BOT - (panels - 1) * GEOM.GAP - panels * lab) / panels;
  return Array.from({ length: panels }, (_, i) => {
    const top = GEOM.TOP + i * (innerH + GEOM.GAP + lab) + lab;
    return { top, bottom: top + innerH, innerH };
  });
}

/** Linear map of the domain [d0, d1] onto the range [r0, r1]. */
export const linear = (d0, d1, r0, r1) => (v) => r0 + ((v - d0) / (d1 - d0 || 1)) * (r1 - r0);
