// Geometry helpers of the canvas pane (port of panel-width.ts and the frame's
// content-height rule). Pure.

export const MIN_SCALE = 0.5; // below this the page text is unreadable: the stage scrolls instead

/** Frame scale for a pane paneWidth px wide: floored so a squeezed pane never mirrors the frame. */
export function previewScale(paneWidth, viewportWidth, min = MIN_SCALE) {
  return Math.max(min, Math.min(1, (paneWidth - 32) / viewportWidth));
}

/** The zoom choices: Fit (null: the scale that fills the pane) and fixed scales. */
export const ZOOMS = { fit: null, 100: 1, 75: 0.75, 50: 0.5 };

/** The frame scale for a zoom mode: Fit follows the pane (down to `min`: a phone-width pane may go below the floor, there is no room to scroll), a fixed zoom does not (never below MIN_SCALE). */
export const zoomScale = (mode, paneWidth, viewportWidth, min = MIN_SCALE) =>
  mode === "fit" || ZOOMS[mode] === undefined ? (paneWidth > 0 ? previewScale(paneWidth, viewportWidth, min) : 1) : Math.max(MIN_SCALE, ZOOMS[mode]);

/** The ancestor chain of node `id` (root first) from the layout nodes' parent ids: [{id, k, t}]. Empty for an unknown id. */
export function crumbs(nodes, id) {
  const out = [];
  const seen = new Set();
  for (let n = nodes.find((x) => x.id === id); n && !seen.has(n.id); n = nodes.find((x) => x.id === n.p)) {
    seen.add(n.id);
    out.unshift({ id: n.id, k: n.k, t: n.t });
  }
  return out;
}

/**
 * The bridge's docHeight is a scrollHeight, which never drops below the
 * iframe's own height, so the canvas could not shrink. Prefer the bottom of the
 * last measured block and fall back to docHeight for a document with none.
 */
export function contentHeight(m) {
  const bottoms = [...m.sections, m.header, m.footer].flatMap((b) => (b ? [b.top + b.height] : []));
  return bottoms.length > 0 ? Math.max(...bottoms) : m.docHeight;
}

/**
 * Where the element toolbar goes (stage px). `box` is the element, `bar` the toolbar size, `view` the visible part of the
 * stage {top, left, right, bottom}. Above the element by `gap`; below when there is no room above (or when `below` is
 * asked for, e.g. while the text formatting bar takes the space above); inside the element's top edge (8px in) when neither fits.
 * Kept inside the view horizontally. `flip` says it went below, `inside` that it sits over the element. Pure.
 */
export function placeToolbar({ box, bar, view, gap = 8, below = false }) {
  const bottom = view.bottom ?? Infinity;
  const above = box.y - bar.h - gap;
  const under = box.y + box.h + gap;
  let y;
  let flip = false;
  let inside = false;
  if (!below && above >= view.top) y = above;
  else if (under + bar.h <= bottom) (y = under), (flip = true);
  else if (above >= view.top) y = above; // `below` was asked for but there is no room under the element
  else (y = Math.max(view.top, Math.min(box.y + gap, bottom - bar.h))), (inside = true); // no room either way: inside the element's top edge
  const x = Math.max(view.left, Math.min(box.x + (inside ? gap : 0), view.right - bar.w));
  return { x, y, flip, inside };
}
