// Path and shape geometry for the AI analytics time chart and sparklines,
// ported from the prototype (timeChart and spark). Inputs are pixel
// coordinates unless a name says values. No DOM, so Node can import it.

// Numbers that go in SVG attributes (not CSS) so they stay identical to the prototype.
export const OPACITY = { stacked: 0.3, single: 0.14, spark: 0.14, compareTotal: 0.45, compareSeries: 0.6 };
export const STROKE = { line: 1.6, compare: 1.25, spark: 1.5, cross: 2.2 };
export const COMPARE_DASH = "3 3";
export const COLUMN_RX = 1.5;
export const MARKER_R = { point: 3, hover: 4.5, single: 4 };

const pt = (x, y) => x.toFixed(1) + "," + y.toFixed(1);
const points = (xs, ys) => xs.map((x, i) => pt(x, ys[i])).join(" L");

/** "M x,y L x,y ..." with one decimal, as the prototype draws every line. */
export const linePath = (xs, ys) => "M" + points(xs, ys);

/** Top edge left to right, then the base edge right to left, closed. */
export const areaPath = (xs, topYs, baseYs) => "M" + points(xs, topYs) + " L" + points([...xs].reverse(), [...baseYs].reverse()) + "Z";

/**
 * Cumulative stacking of series values (bottom first): per series
 * {base, top} arrays by index, so series k sits on series k-1.
 */
export function stackBaselines(seriesValues) {
  const n = seriesValues.reduce((m, v) => Math.max(m, v.length), 0);
  let base = Array(n).fill(0);
  return seriesValues.map((values) => {
    const top = base.map((b, i) => b + (values[i] || 0));
    const out = { base, top };
    base = top;
    return out;
  });
}

/** Column width: 60% of the slot, clamped to 2..18 px. */
export const columnWidth = (innerW, n) => Math.max(2, Math.min(18, (innerW / n) * 0.6));

/** Bars for the points above the baseline: [{x, y, w, h}] (empty ones skipped). */
export function columns(xs, ys, baseY, innerW, n) {
  const w = columnWidth(innerW, n);
  const out = [];
  xs.forEach((x, i) => {
    const h = baseY - ys[i];
    if (h > 0) out.push({ x: +(x - w / 2).toFixed(1), y: +ys[i].toFixed(1), w: +w.toFixed(1), h: +h.toFixed(1) });
  });
  return out;
}

/**
 * Indexes that get a small marker. Fewer than three visible series draw none
 * (the lines are readable alone); otherwise every second x-tick, counted back
 * from the last point, so the last point always has one.
 */
export function markerIndexes(n, visibleSeries, step = 1) {
  if (visibleSeries < 3) return [];
  const out = [];
  for (let i = n - 1, k = 0; i >= 0; i -= step, k++) if (k % 2 === 0) out.unshift(i);
  return out;
}

/**
 * Sparkline in a 100 x `height` viewBox (the hm/spark box is 100 x 24): {line, area}
 * (points / path data), empty under two values. Zero based unless `minmax`, which
 * stretches min..max. Two units of padding at the foot, the same at the head.
 */
export function sparkPoints(vals, { minmax = false, height = 24 } = {}) {
  const n = vals.length;
  if (n < 2) return { line: "", area: "" };
  const lo = minmax ? Math.min(...vals) : 0;
  const range = Math.max(minmax ? 1e-9 : 1, Math.max(...vals) - lo);
  const amp = height - (minmax ? 7 : 4);
  const pts = vals.map((v, i) => ((i / (n - 1)) * 100).toFixed(1) + "," + (height - 2 - ((v - lo) / range) * amp).toFixed(1));
  return { line: pts.join(" "), area: "M0," + height + " L" + pts.join(" L") + " L100," + height + "Z" };
}
