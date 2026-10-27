// The "+" insertion points of the canvas (a lazy chunk, used by canvas/chrome.js). Pure.

/**
 * The "+" insertion points of the layout nodes (document px). `parents` is the set of container ids ("" is the page)
 * whose children get points between and around them (its order is the priority when points crowd); a container with no
 * child gets one inside it whatever `parents` says. Each point: {key, parent, index, after, before (sibling ids around it,
 * or null), x, y (where the button sits), len (the insertion line), dir ("column": a horizontal line across stacked
 * children, "row": a vertical line between side-by-side ones), empty}. The nodes arrive in document order, so the
 * children of a parent are its siblings in order.
 *
 * A point sits in the gap between its two neighbours, or at the edge of its one neighbour, halfway to the container's own
 * edge: never inside a child's text. In a row of wrapped children (a grid, a wrapping flex row) a point sits between two
 * items only when they share a line; at a line end it sits at the end of that line's last item. `gap` (document px, 0 = no
 * limit) is the least distance between two points: the one of lower priority (empty, then the order of `parents`) goes.
 * Pure.
 */
export function insertionPoints(nodes, parents, { gap = 0 } = {}) {
  const byParent = new Map();
  for (const n of nodes) byParent.set(n.p, [...(byParent.get(n.p) ?? []), n]);
  const out = [];
  const rank = new Map([...parents].map((id, i) => [id, i]));
  const at = (parent, kids, host, dir, i) => {
    const prev = kids[i - 1] ?? null;
    const next = kids[i] ?? null;
    const row = dir === "row";
    const lo = (n) => (row ? n.x : n.y);
    const hi = (n) => (row ? n.x + n.w : n.y + n.h);
    const across = (n) => (row ? [n.y, n.y + n.h] : [n.x, n.x + n.w]);
    const sameLine = prev && next && (row ? prev.y < next.y + next.h && next.y < prev.y + prev.h : true);
    // along the layout direction: in the gap, at a line end, or halfway to the container's edge at the ends
    let pos;
    if (sameLine) pos = (hi(prev) + lo(next)) / 2;
    else if (!prev) pos = host ? (lo(host) + lo(next)) / 2 : lo(next);
    else if (!next) pos = host ? (hi(prev) + hi(host)) / 2 : hi(prev);
    else pos = hi(prev); // two items on different lines: the end of prev's line
    const near = sameLine ? [prev, next] : [prev ?? next];
    const span = [Math.min(...near.map((n) => across(n)[0])), Math.max(...near.map((n) => across(n)[1]))];
    out.push({ key: `${parent}:${i}`, parent, index: i, after: prev?.id ?? null, before: next?.id ?? null, x: row ? pos : (span[0] + span[1]) / 2, y: row ? (span[0] + span[1]) / 2 : pos, len: span[1] - span[0], dir, empty: false, rank: rank.get(parent) ?? 0 });
  };
  const dirOf = (n) => (n.dir === "column" || !n.dir ? "column" : "row"); // a grid lays its children out along the row
  const roots = byParent.get("") ?? [];
  if (parents.has("") && roots.length) for (let i = 0; i <= roots.length; i++) at("", roots, null, "column", i);
  for (const n of nodes) {
    if (n.c !== 1) continue;
    const kids = byParent.get(n.id) ?? [];
    if (!kids.length) out.push({ key: `${n.id}:0`, parent: n.id, index: 0, after: null, before: null, x: n.x + n.w / 2, y: n.y + n.h / 2, len: 0, dir: dirOf(n), empty: true, rank: -1 });
    else if (parents.has(n.id)) for (let i = 0; i <= kids.length; i++) at(n.id, kids, n, dirOf(n), i);
  }
  if (!gap) return out;
  const kept = [];
  for (const pt of [...out].sort((a, b) => a.rank - b.rank)) if (kept.every((k) => Math.hypot(k.x - pt.x, k.y - pt.y) >= gap)) kept.push(pt);
  const keep = new Set(kept);
  return out.filter((pt) => keep.has(pt));
}
