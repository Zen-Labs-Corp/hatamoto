// Series order, dash patterns and marker shapes for the AI analytics charts.
// Colour is NOT read here: elements carry the class `hm-viz--series-<n>`
// (which sets --viz) so light and dark follow the theme through CSS.
// No DOM, so Node can import it.

// The prototype draws c1,c2,c3,c5,c4,c6: violet (3) and fuchsia (4) are never
// neighbours. The last entry (chart 6, gray) is also the "Other" series.
export const SERIES = [
  { n: 1, dash: "", marker: "circle" },
  { n: 2, dash: "5 3", marker: "square" },
  { n: 3, dash: "2 3", marker: "triangle" },
  { n: 5, dash: "1 3", marker: "cross" },
  { n: 4, dash: "8 3 2 3", marker: "diamond" },
  { n: 6, dash: "10 4", marker: "circle" },
];

/** Series for the i-th dataset; "Other" is always the last slot. */
export const seriesFor = (i, isOther) => (isOther ? SERIES[5] : SERIES[i % 5]);

export const vizClass = (s) => `hm-viz--series-${s.n}`;

const fixed = (v) => +v.toFixed(1);

/**
 * Marker geometry as {tag, attrs} for circle|square|triangle|diamond|cross.
 * The caller creates the element and adds the class; fill and stroke come
 * from CSS (`hm-aia-marker`, `hm-aia-marker--cross`).
 */
export function markerPath(kind, cx, cy, r) {
  const x = fixed(cx);
  const y = fixed(cy);
  switch (kind) {
    case "square":
      return { tag: "rect", attrs: { class: "hm-aia-marker", x: x - r, y: y - r, width: 2 * r, height: 2 * r, "stroke-width": 1 } };
    case "triangle":
      return { tag: "polygon", attrs: { class: "hm-aia-marker", points: `${x},${y - r - 1} ${x + r + 1},${y + r} ${x - r - 1},${y + r}`, "stroke-width": 1 } };
    case "diamond":
      return { tag: "polygon", attrs: { class: "hm-aia-marker", points: `${x},${y - r - 1} ${x + r + 1},${y} ${x},${y + r + 1} ${x - r - 1},${y}`, "stroke-width": 1 } };
    case "cross":
      return { tag: "path", attrs: { class: "hm-aia-marker hm-aia-marker--cross", d: `M${x - r} ${y - r}L${x + r} ${y + r}M${x + r} ${y - r}L${x - r} ${y + r}`, "stroke-width": 2.2 } };
    default:
      return { tag: "circle", attrs: { class: "hm-aia-marker", cx: x, cy: y, r, "stroke-width": 1 } };
  }
}
