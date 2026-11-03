// What the canvas chrome shows, as data: the toolbar's contents, the "Add section" boundaries, the size badge text and the
// padding hatch rectangles. Pure (no DOM, no screen import) so the shapes are testable; chrome.js and direct.js draw them.

/**
 * The element toolbar. info (screen's toolbarState): {name, kind, parentId, parentName, canUp, canDown, hidden, editing}.
 * -> {title: {icon, name}, up: {id, text, aria, title} | null, buttons: [{act, icon, aria, disabled?}]}. No buttons while text is edited.
 */
export function toolbarModel(info) {
  const name = info.name || info.kind || "";
  const parent = info.parentName || "parent";
  const button = (act, icon, aria, disabled) => ({ act, icon, aria, ...(disabled ? { disabled: true } : {}) });
  return {
    title: { icon: info.kind, name },
    up: info.parentId ? { id: info.parentId, text: `↑ ${parent}`, aria: `${name} ↑ ${parent}, select parent`, title: `Select parent, ${parent}` } : null,
    buttons: info.editing
      ? []
      : [
          button("moveup", "arrow-up", "Move up", !info.canUp),
          button("movedown", "arrow-down", "Move down", !info.canDown),
          button("duplicate", "copy", "Duplicate"),
          button("togglehide", info.hidden ? "eye" : "eye-off", info.hidden ? "Show on this device" : "Hide on this device"),
          button("remove", "trash", "Delete"),
          button("more", "more", "More element actions"),
        ],
  };
}

/**
 * The boundaries between top-level elements (layout nodes with p === ""), top to bottom: n elements give n + 1.
 * -> [{y, before, after}]: y in document px (the gap's middle, the page's top, the last element's bottom), before the id above and
 * after the id below (null at the ends). An empty page has one boundary.
 */
export function pillPoints(nodes) {
  const tops = nodes.filter((n) => n.p === "").sort((a, b) => a.y - b.y);
  if (!tops.length) return [{ y: 0, before: null, after: null }];
  const out = [{ y: tops[0].y, before: null, after: tops[0].id }];
  for (let i = 1; i < tops.length; i++) out.push({ y: (tops[i - 1].y + tops[i - 1].h + tops[i].y) / 2, before: tops[i - 1].id, after: tops[i].id });
  const last = tops[tops.length - 1];
  out.push({ y: last.y + last.h, before: last.id, after: null });
  return out;
}

/**
 * The doc.nodes index a boundary inserts at: 0 at the top, `total` at the bottom, else the doc index of the element below it.
 * indexOf(id) is the doc's own order (sectionOf(doc, id).index), never the frame's, which skips nodes it does not render.
 * null when the element below is unknown to the doc.
 */
export function boundaryIndex(boundary, indexOf, total) {
  if (boundary.before === null) return 0;
  if (boundary.after === null) return total;
  const i = indexOf(boundary.after);
  return Number.isInteger(i) ? i : null;
}

/** "1280 × 412": the page's CSS px, rounded, whatever the zoom. */
export const sizeText = (box) => `${Math.round(box.w)} × ${Math.round(box.h)}`;

/**
 * The padding bands of a box, relative to its top-left, in document px: [{side, x, y, w, h}] for top, right, bottom, left.
 * Top and bottom span the full width; left and right fill the height between them. pd is [t, r, b, l] (missing = 0).
 */
export function hatchRects(box, pd) {
  const [t, r, b, l] = [0, 1, 2, 3].map((i) => Math.max(0, pd?.[i] || 0));
  const mid = Math.max(0, box.h - t - b);
  return [
    { side: "top", x: 0, y: 0, w: box.w, h: t },
    { side: "right", x: box.w - r, y: t, w: r, h: mid },
    { side: "bottom", x: 0, y: box.h - b, w: box.w, h: b },
    { side: "left", x: 0, y: t, w: l, h: mid },
  ];
}

/**
 * Where the size badge hangs under a selection: its x (px, any unit the box is in), always the box's middle. The selection's name tag hangs from the
 * box's left edge on the same row: when the two would touch (a small element), `below` says the badge drops to a second row.
 */
export const sizeAnchor = (box, tagW = 0, badgeW = 0) => {
  const x = box.x + box.w / 2;
  return { x, align: "center", below: tagW > 0 && x - badgeW / 2 < box.x + tagW + 4 };
};

/** A handle's centre kept so a handle `half` px thick stays inside 0..max: padding 0 puts its centre on the frame's edge, where it would be cut in half. */
export const keepInside = (centre, half, max) => Math.max(half, Math.min(centre, Math.max(half, max - half)));

const clear = (r, o, gap) => r.x + r.w + gap <= o.x || o.x + o.w + gap <= r.x || r.y + r.h + gap <= o.y || o.y + o.h + gap <= r.y;

/**
 * How far a selected section's "Add section" pill moves off its row (px, + is down) so it never touches what else sits there: the padding handles, the W x H badge and
 * the toolbar. `pill` is its rect at the row's right end, `obstacles` the rects of those, all in one space. 0 when the row is clear with `gap` to spare. Otherwise the nearest
 * spot just outside an obstacle that clears every one, the `outward` side (+1 below a bottom edge, -1 above a top edge) favoured by a pill's height.
 */
export function pillShift(pill, obstacles, outward, gap = 4) {
  const free = (dy) => obstacles.every((o) => clear({ ...pill, y: pill.y + dy }, o, gap));
  if (free(0)) return 0;
  const spots = obstacles.flatMap((o) => [o.y + o.h + gap - pill.y, o.y - gap - pill.h - pill.y]).filter(free);
  const cost = (d) => Math.abs(d) + (Math.sign(d) === outward ? 0 : pill.h); // the nearest spot, the outward side favoured by a pill's height
  return spots.sort((x, y) => cost(x) - cost(y))[0] ?? 0;
}
